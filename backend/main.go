// 加密备份 - 后端服务
// 通过飞牛统一网关（Unix Socket）对外提供 API 与静态 UI。
// 功能：restic 备份任务管理、内置调度器、进度解析、日志、仓库管理。
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// appVersion 由构建时注入：-ldflags "-X main.appVersion=x.y.z"（见 build.sh）。
// 未注入时（Docker 复用产物、手工 go build）回落到同目录 VERSION 或 fpk 的 manifest，
// 保证 /api/status 暴露的版本号始终与包一致，不会再出现代码是新的、版本号还停在老版本。
var appVersion = "dev"

func init() { appVersion = resolveVersion() }

// readVersionFile 读取 "version = 0.2.8" 形式的 manifest，或纯文本的 VERSION 文件
func readVersionFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			if strings.TrimSpace(k) != "version" {
				continue
			}
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
		return strings.Trim(strings.TrimSpace(line), `"'`) // 纯文本文件：第一行即版本
	}
	return ""
}

func resolveVersion() string {
	if appVersion != "" && appVersion != "dev" {
		return appVersion
	}
	if v := strings.TrimSpace(os.Getenv("APP_VERSION")); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{
			filepath.Join(dir, "VERSION"),
			filepath.Join(dir, "manifest"),
			filepath.Join(dir, "..", "manifest"),
			filepath.Join(dir, "..", "..", "manifest"),
		} {
			if v := readVersionFile(p); v != "" {
				return v
			}
		}
	}
	return "unknown"
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func varDir() string { return env("TRIM_PKGVAR", "./var") }

// ------------------------------------------------------------------
// 身份与授权目录（飞牛统一网关）
//
// 网关在确认登录后会把用户身份放进 Header 转发给应用：
//   X-Trim-Userid / X-Trim-Username / X-Trim-Isadmin
// 管理员在「应用中心 → 应用设置 → 授权目录」勾选的路径，则通过环境变量
// TRIM_DATA_ACCESSIBLE_PATHS（冒号分隔）传进来。
//
// 所以「能访问哪些目录」的规则是：
//  1) USER_ROOT 环境变量：Docker / 手工部署显式指定，没有网关身份可依赖，只认它
//  2) 硬边界：当前登录用户自己的家目录 /volN/<uid> —— 永远不会跨到别人账号
//  3) 在此之内的收窄：管理员勾选的授权目录若与本人主目录有交集，就只保留交集
//  4) 网关没给身份（裸端口）时才兜底 /vol1/1000
//
// 也就是说 A 账号只可能看到 A 自己的目录，B 账号只看到 B 的；
// 授权目录只能在本人家目录内部进一步缩小范围，不能把别人账号的目录放进来。
//
// 注意：Header 只在 socket 模式（真有网关在前面）采信。裸端口形态没有网关，
// 任何人都能伪造这几个 Header，采信就等于开了提权后门。
//
// 规则：任何来自请求的本地路径，必须先经 resolveSafePath 规范化并确认落在
// 允许的根目录之内，否则一律拒绝。符号链接会被解析后再判定，避免用 symlink
// 绕过前缀检查。
// ------------------------------------------------------------------

const defaultUserRoot = "/vol1/1000"

func socketMode() bool { return os.Getenv("SOCKET_PATH") != "" }

// identity 当前请求的登录用户（由网关 Header 提供）
type identity struct {
	UID   int
	Name  string
	Admin bool
	OK    bool
}

func identityFrom(r *http.Request) identity {
	var id identity
	if r == nil || !socketMode() {
		return id
	}
	if v := strings.TrimSpace(r.Header.Get("X-Trim-Userid")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			id.UID, id.OK = n, true
		}
	}
	id.Name = strings.TrimSpace(r.Header.Get("X-Trim-Username"))
	id.Admin = strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Trim-Isadmin")), "true")
	return id
}

// envAuthorizedPaths：启动时由应用中心快照进环境变量的授权目录。
// 只在动态查询拿不到结果时兜底使用。
func envAuthorizedPaths() []string {
	raw := strings.TrimSpace(os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS"))
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ":") {
		if p = strings.TrimSpace(p); p == "" || !filepath.IsAbs(p) {
			continue
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}

// ------------------------------------------------------------------
// 授权目录热更新
//
// 平台把授权目录持久化在 appcenter 库的 app_authorization_path 表里，但只在
// 「启动应用」那一刻快照成环境变量 TRIM_DATA_ACCESSIBLE_PATHS，所以过去改完
// 授权必须重启应用才生效。
//
// 这里改成：后台每隔几秒只读查一次那张表（应用以 root 运行，postgres 的
// peer 认证免密，psql 走 unix socket），把结果放进内存缓存，newScope 优先用
// 缓存。于是管理员在应用中心改完授权，最迟一个轮询周期就生效，无需重启。
//
// 安全边界不变：
//  · 只发一条固定 SELECT，应用名做白名单校验，不接受任何外部输入拼接；
//  · 查询失败 / 查不到（Docker 等非 fpk 形态）时回退到启动快照，功能不退化；
//  · 拿不到授权列表时退回「本人家目录」，绝不因为读不到就放大到更宽的范围。
// ------------------------------------------------------------------

var (
	scopeMu      sync.RWMutex
	scopeCache   []string // 最近一次成功查到的授权目录
	scopeLoaded  bool     // 是否成功查过至少一次（成功查到空列表也算）
	scopeSyncAt  time.Time
	scopeLastErr string

	// 允许的应用名：写进 SQL 前必须先过这道校验，杜绝拼接注入
	appNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// appName 当前应用名，由平台注入（TRIM_APPNAME），缺省用包名
func appName() string {
	if v := strings.TrimSpace(os.Getenv("TRIM_APPNAME")); v != "" {
		return v
	}
	return "encbackup"
}

// psqlBinary 定位系统自带的 psql（不同飞牛版本 postgres 主版本号可能不同）
func psqlBinary() string {
	if cands, err := filepath.Glob("/usr/lib/postgresql/*/bin/psql"); err == nil && len(cands) > 0 {
		sort.Strings(cands)
		return cands[len(cands)-1]
	}
	if cands, err := filepath.Glob("/usr/pgsql-*/bin/psql"); err == nil && len(cands) > 0 {
		sort.Strings(cands)
		return cands[len(cands)-1]
	}
	if p, err := exec.LookPath("psql"); err == nil {
		return p
	}
	return ""
}

// queryAuthorizedPaths 只读查询平台授权目录表
func queryAuthorizedPaths() ([]string, error) {
	name := appName()
	if !appNamePattern.MatchString(name) {
		return nil, errors.New("应用名不合法")
	}
	psql := psqlBinary()
	if psql == "" {
		return nil, errors.New("未找到 psql")
	}
	// 只查「应用级」授权（应用设置 → 授权目录 里勾选的那些）。
	// 用户级授权表 app_user_authorization_path 是「允许用户自行授权」用的，
	// 与本应用「只见自己家目录」的隔离模型不冲突，这里不参与判定。
	sql := fmt.Sprintf(`select distinct p.path from app_authorization_path p `+
		`join app a on a.id = p.app_id where a.app_name = '%s'`, name)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, psql, "-U", "postgres", "-d", "appcenter", "-tAc", sql)
	cmd.Env = append(os.Environ(), "PGOPTIONS=-c statement_timeout=4000")
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			if s := strings.TrimSpace(string(ee.Stderr)); s != "" {
				msg = s
			}
		}
		return nil, errors.New(msg)
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !filepath.IsAbs(line) {
			continue
		}
		paths = append(paths, filepath.Clean(line))
	}
	return paths, nil
}

// refreshAuthorizedPaths 查一次并更新缓存，返回是否发生变化
func refreshAuthorizedPaths() (bool, error) {
	paths, err := queryAuthorizedPaths()
	scopeMu.Lock()
	defer scopeMu.Unlock()
	if err != nil {
		// 保留上一次成功的结果，只记录错误，避免瞬时失败把权限放大
		scopeLastErr = err.Error()
		return false, err
	}
	changed := !scopeLoaded || !sameStrings(scopeCache, paths)
	scopeCache, scopeLoaded, scopeSyncAt, scopeLastErr = paths, true, time.Now(), ""
	return changed, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// authorizedPaths 当前生效的授权目录：优先用热更新缓存，回退启动快照
func authorizedPaths() []string {
	scopeMu.RLock()
	loaded, cached := scopeLoaded, scopeCache
	scopeMu.RUnlock()
	if loaded {
		return append([]string(nil), cached...)
	}
	return envAuthorizedPaths()
}

// 诊断用：是否已从平台实时读到授权目录 / 最后一次同步时间 / 最近一次错误
func authLive() bool {
	scopeMu.RLock()
	defer scopeMu.RUnlock()
	return scopeLoaded
}

func authSyncedAt() string {
	scopeMu.RLock()
	defer scopeMu.RUnlock()
	if scopeSyncAt.IsZero() {
		return ""
	}
	return scopeSyncAt.Format(time.RFC3339)
}

func authErrText() string {
	scopeMu.RLock()
	defer scopeMu.RUnlock()
	return scopeLastErr
}

// authWatcher 后台盯着授权表，改完授权不用重启应用
func authWatcher() {
	const interval = 8 * time.Second
	var lastErr string
	tick := func() {
		changed, err := refreshAuthorizedPaths()
		if err != nil {
			// 同样的错误只记一次，避免刷爆日志
			if err.Error() != lastErr {
				lastErr = err.Error()
				log.Printf("[scope] 授权目录查询失败（继续沿用上一次结果）：%v", err)
			}
			return
		}
		lastErr = ""
		if changed {
			scopeMu.RLock()
			cur := append([]string(nil), scopeCache...)
			scopeMu.RUnlock()
			log.Printf("[scope] 授权目录已更新：%v", cur)
		}
	}
	tick()
	for {
		time.Sleep(interval)
		tick()
	}
}

// homeDirsFor：指定用户在各卷上的家目录（存在才纳入），实现多用户隔离
func homeDirsFor(uid int) []string {
	if uid <= 0 {
		return nil
	}
	sub := strconv.Itoa(uid)
	var out []string
	for i := 1; i <= 9; i++ {
		cand := filepath.Join("/vol"+strconv.Itoa(i), sub)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			out = append(out, cand)
		}
	}
	return out
}

// accessScope 本次请求可访问的范围
type accessScope struct {
	Roots      []string // 已解析符号链接，用于前缀比较
	Display    []string // 原始路径，用于界面展示
	Source     string   // env / authorized / home / default
	ID         identity
	Authorized []string
	Home       string // 本人家目录（用于前端展示相对路径），拿不到时为空
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// newScope 计算「本次请求能访问哪些目录」。
//
// 核心不变式：可见范围永远是本人家目录的子集，任何情况下都不会跨账号。
// 授权目录的作用只是「在本人家目录内部再缩小」，不会扩大。
func newScope(r *http.Request) accessScope {
	sc := accessScope{ID: identityFrom(r)}
	sc.Authorized = authorizedPaths()

	// ① 显式指定（Docker / 手工部署，没有网关可依赖）
	if v := strings.TrimSpace(os.Getenv("USER_ROOT")); v != "" {
		sc.Display = []string{filepath.Clean(v)}
		sc.Source = "env"
		return sc.finish()
	}

	// ② 定出本人家目录（硬边界）
	home := homeDirsFor(sc.ID.UID)
	if len(home) > 0 {
		sc.Home = home[0]
	}
	if len(home) == 0 {
		if sc.ID.UID > 0 {
			// 有身份但该用户在任何卷上都没有家目录：给一个必然不存在的根，
			// 宁可什么都访问不到，也绝不落到别人账号的目录上
			home = []string{filepath.Join("/vol1", strconv.Itoa(sc.ID.UID))}
		} else {
			// 没有身份（裸端口部署）：兜底，行为与旧版一致
			sc.Display = []string{defaultUserRoot}
			sc.Source = "default"
			return sc.finish()
		}
	}

	// ③ 授权目录只保留「属于自己的」，非空则收窄
	if inter := ownAuthorized(home, sc.Authorized); len(inter) > 0 {
		sc.Display = inter
		sc.Source = "authorized"
		return sc.finish()
	}

	sc.Display = home
	sc.Source = "home"
	return sc.finish()
}

// ownAuthorized 取「授权目录 ∩ 本人主目录」：只保留落在自己家目录内的授权项。
// 管理员勾了别人账号的目录时，那一条对本账号无效。
func ownAuthorized(home, auth []string) []string {
	if len(home) == 0 || len(auth) == 0 {
		return nil
	}
	realHome := make([]string, 0, len(home))
	for _, h := range home {
		realHome = append(realHome, realPath(h))
	}
	var out []string
	for _, a := range auth {
		ra := realPath(a)
		for _, rh := range realHome {
			if withinRoot(ra, rh) {
				out = append(out, filepath.Clean(a))
				break
			}
		}
	}
	return out
}

// finish 把展示路径转成用于前缀比较的真实路径
func (sc accessScope) finish() accessScope {
	if len(sc.Display) == 0 {
		sc.Display = []string{defaultUserRoot}
	}
	sc.Roots = sc.Roots[:0]
	for _, d := range sc.Display {
		sc.Roots = append(sc.Roots, realPath(d))
	}
	return sc
}

// withinRoot 判断 p 是否等于 root 或位于 root 之下（按路径分隔符边界判断，
// 避免 /vol1/10001 被误判为 /vol1/1000 的子路径）
func withinRoot(p, root string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(os.PathSeparator))
}

// resolveSafePath 校验并规范化用户提交的本地路径。
// 校验在「符号链接解析后」的真实路径上做，返回的却是清理过的原始路径
// （保持与前端展示的 user_root 一致，避免把 /private/tmp 之类真身暴露到界面）。
// 允许范围随请求变化：平台授权目录 / 当前登录用户家目录 / USER_ROOT 覆盖。
// 越界或非法则返回 error。
func resolveSafePath(r *http.Request, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("路径不能为空")
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("路径必须是绝对路径")
	}
	clean := filepath.Clean(p)

	// 解析符号链接：路径存在则整体解析；不存在则解析最近的存在祖先，
	// 再把剩余部分接回去（用于「目标目录还没创建」的还原场景）。
	resolved := clean
	if r, err := filepath.EvalSymlinks(clean); err == nil {
		resolved = r
	} else {
		dir := clean
		for {
			if r, err := filepath.EvalSymlinks(dir); err == nil {
				if rest, rerr := filepath.Rel(dir, clean); rerr == nil && rest != "." {
					resolved = filepath.Join(r, rest)
				} else {
					resolved = r
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	sc := newScope(r)
	for _, root := range sc.Roots {
		if withinRoot(resolved, root) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("路径超出允许范围（当前可访问 %s）", strings.Join(sc.Display, "、"))
}

// safeIncludes 校验 restic 还原时的 include 模式，拒绝任何试图跳出快照根的写法
func safeIncludes(includes []string) error {
	for _, inc := range includes {
		if inc == "" {
			return errors.New("还原路径不能为空")
		}
		for _, seg := range strings.Split(inc, "/") {
			if seg == ".." {
				return fmt.Errorf("还原路径含非法片段：%s", inc)
			}
		}
	}
	return nil
}

func binPath(name string) string {
	// 显式指定优先（Docker 部署可设 ENGINES_DIR=/app/engines）
	if d := os.Getenv("ENGINES_DIR"); d != "" {
		return filepath.Join(d, name)
	}
	// 默认：fpk 布局 <app>/server/engines；不存在则退回二进制同级 engines/（Docker 布局）
	if _, err := os.Stat(filepath.Join(env("TRIM_APPDEST", "."), "server", "engines", name)); err == nil {
		return filepath.Join(env("TRIM_APPDEST", "."), "server", "engines", name)
	}
	// 必须绝对路径：restic 0.18+ 拒绝隐式执行相对路径的可执行文件
	p := filepath.Join(filepath.Dir(os.Args[0]), "engines", name)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func engineVersion(name string) string {
	out, err := exec.Command(binPath(name), "version").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		return fields[1]
	}
	return line
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// 数据模型
// ---------------------------------------------------------------------------

type Repo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URI      string `json:"uri"`
	Password string `json:"password"` // 输出时掩码
	Cloud    string `json:"cloud"`    // tianyi / y115 / webdav / other
	// WebDAV 直连仓库：凭据写入 rclone.conf（pass 为 rclone obscure 后的密文）
	Remote    string `json:"remote,omitempty"`     // rclone 远端名（wd-xxxxxxxx）
	DavURL    string `json:"dav_url,omitempty"`    // 服务器地址
	DavUser   string `json:"dav_user,omitempty"`   // 账号
	DavPass   string `json:"dav_pass,omitempty"`   // 密码（输出时掩码）
	DavVendor string `json:"dav_vendor,omitempty"` // other / owncloud / nextcloud / sharepoint
	Inited    bool   `json:"inited,omitempty"`     // restic init 已成功完成
}

type Task struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Paths        []string `json:"paths"`
	Excludes     []string `json:"excludes"`
	RepoID       string   `json:"repo_id"`
	Password     string   `json:"password,omitempty"`    // 加密密码（新建任务时生成/填写；实际存到对应 repo，此处仅用于创建时透传与回显掩码）
	DestCloud    string   `json:"dest_cloud,omitempty"`  // 目的地两步式：tianyi / y115
	DestSubdir   string   `json:"dest_subdir,omitempty"` // 云端目标目录
	SchedMode    string   `json:"sched_mode"`            // manual | daily | weekly | monthly | interval
	DailyTime    string   `json:"daily_time"`            // 执行时间 "03:00"（daily/weekly/monthly 共用）
	IntervalHrs  int      `json:"interval_hrs"`
	DailyDays    int      `json:"daily_days"`   // daily: 每 N 天（0/1 = 每天）
	WeeklyDays   []int    `json:"weekly_days"`  // weekly: 0=周日 … 6=周六
	MonthlyDays  []int    `json:"monthly_days"` // monthly: 1-31，-1=末天
	KeepLast     int      `json:"keep_last"`    // restic forget --keep-last, 0=不清理
	Enabled      bool     `json:"enabled"`
	CreatedAt    string   `json:"created_at"`
	LastRunAt    string   `json:"last_run_at"`
	LastDoneAt   string   `json:"last_done_at"` // 上次成功结束时间（调度去重用）
	LastStatus   string   `json:"last_status"`  // success | failed | running | stopped | ""
	LastMessage  string   `json:"last_message"`
	LastFiles    int64    `json:"last_files"`
	LastBytes    int64    `json:"last_bytes"`
	LastSnapID   string   `json:"last_snapshot_id"`
	LastDuration int      `json:"last_duration_sec"`
	NextRunAt    string   `json:"next_run_at,omitempty"` // 下次运行时间（计算填充，不落盘）
	AuthWarn     string   `json:"auth_warn,omitempty"`   // 云盘授权失效告警（运行时探测填充，不落盘）
}

type Config struct {
	Repos    []Repo       `json:"repos"`
	Tasks    []Task       `json:"tasks"`
	Restores []RestoreJob `json:"restores,omitempty"` // 还原历史（最近 30 条）
}

// RestoreJob：一次还原执行
type RestoreJob struct {
	ID           string   `json:"id"`
	TaskID       string   `json:"task_id"`
	TaskName     string   `json:"task_name"`
	Snapshot     string   `json:"snapshot"` // restic 快照 ID（短）
	SnapshotTime string   `json:"snapshot_time,omitempty"`
	Dest         string   `json:"dest"`             // original | custom
	Target       string   `json:"target,omitempty"` // custom：目标目录
	Conflict     string   `json:"conflict"`         // always | if-changed | never | rename
	Includes     []string `json:"includes"`         // 要还原的绝对路径
	Status       string   `json:"status"`           // running | success | failed | stopped
	Message      string   `json:"message,omitempty"`
	StartedAt    string   `json:"started_at"`
	FinishedAt   string   `json:"finished_at,omitempty"`
	Pct          float64  `json:"pct"`
	Files        int64    `json:"files"`
	Bytes        int64    `json:"bytes"`
	Restored     int      `json:"restored"` // 新增（原不存在）
	Replaced     int      `json:"replaced"` // 覆盖
	Renamed      int      `json:"renamed"`  // 保留两者（重命名）
	Skipped      int      `json:"skipped"`  // 跳过
}

// ---------------------------------------------------------------------------
// 全局状态
// ---------------------------------------------------------------------------

var (
	cfgMu    sync.Mutex
	cfg      = &Config{}
	stateMu  sync.Mutex
	running  = ""          // 正在运行的任务 ID
	runStop  chan struct{} // 停止信号
	runStart string        // 当前运行开始时间（RFC3339）
	runPct   float64
	runFiles int64
	runBytes int64
	runTotal int64
	runCur   []string
	runErrs  int64
	runLog   string // 当前运行日志文件路径
)

// 云盘授权探测缓存：key=远端名(ol:xxx)，value=探测结论与时间。避免每 30s 调度都跑一遍 rclone。
type authProbe struct {
	ok      bool
	hint    string
	checked time.Time
}

var (
	authMu    sync.Mutex
	authCache = map[string]authProbe{}
)

func maskPasswords() {
	for i := range cfg.Repos {
		if cfg.Repos[i].Password != "" {
			cfg.Repos[i].Password = "******"
		}
		if cfg.Repos[i].DavPass != "" {
			cfg.Repos[i].DavPass = "******"
		}
	}
}

func unmaskPassword(r Repo, stored *Repo) Repo {
	if r.Password == "******" || r.Password == "" {
		r.Password = stored.Password
	}
	if r.DavPass == "******" || r.DavPass == "" {
		r.DavPass = stored.DavPass
	}
	return r
}

func saveConfig() error {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	p := filepath.Join(varDir(), "config.json")
	return os.WriteFile(p, data, 0o600)
}

func loadConfig() {
	data, err := os.ReadFile(filepath.Join(varDir(), "config.json"))
	if err == nil {
		_ = json.Unmarshal(data, cfg)
	}
	// 启动自愈：上次进程退出时残留的 running 状态清掉（实际进程已不在）
	dirty := false
	for i := range cfg.Tasks {
		if cfg.Tasks[i].LastStatus == "running" {
			cfg.Tasks[i].LastStatus = "failed"
			cfg.Tasks[i].LastMessage = "应用重启时中断，可手动重新运行"
			dirty = true
		}
	}
	for i := range cfg.Restores {
		if cfg.Restores[i].Status == "running" {
			cfg.Restores[i].Status = "failed"
			cfg.Restores[i].Message = "应用重启时中断"
			dirty = true
		}
	}
	if dirty {
		_ = saveConfig()
	}
}

func findRepo(id string) *Repo {
	for i := range cfg.Repos {
		if cfg.Repos[i].ID == id {
			return &cfg.Repos[i]
		}
	}
	return nil
}

func findTask(id string) *Task {
	for i := range cfg.Tasks {
		if cfg.Tasks[i].ID == id {
			return &cfg.Tasks[i]
		}
	}
	return nil
}

// olRemote：返回仓库的 OpenList 中转远端名（如 "ol:115"），非 ol 中转返回空串。
func olRemote(repo *Repo) string {
	if strings.HasPrefix(repo.URI, "rclone:ol:") {
		return strings.TrimPrefix(repo.URI, "rclone:")
	}
	if strings.HasPrefix(repo.URI, "ol:") {
		return repo.URI
	}
	return ""
}

// probeCloudAuth：探测云盘授权是否有效。仅对走 OpenList 中转的仓库（uri 以 ol: 开头）有意义。
// 用 rclone rcat 上传一个探测文件再删除，判断「写入能力」——列目录对授权失效不敏感
// （115 授权失效时列目录仍返回 directory not found，只有写入才报 500）。
// 带缓存（5 分钟），避免频繁探测。返回 (ok, hint)。
func probeCloudAuth(repo *Repo) (bool, string) {
	remote := olRemote(repo)
	if remote == "" {
		return true, "" // 非 ol 中转（WebDAV 直连）不探测
	}

	authMu.Lock()
	if p, ok := authCache[remote]; ok && time.Since(p.checked) < 5*time.Minute {
		res := p
		authMu.Unlock()
		return res.ok, res.hint
	}
	authMu.Unlock()

	// 探测文件写到仓库子目录下（避免污染云盘根），用唯一名避免并发冲突
	probeDir := remote
	if sub := strings.TrimPrefix(strings.TrimPrefix(repo.URI, "rclone:"), remote); sub != "" {
		probeDir = remote + "/" + strings.Trim(sub, "/")
	}
	probeName := "_auth_probe_" + randomID()
	probePath := probeDir + "/" + probeName
	cmd := exec.Command(binPath("rclone"), "rcat", probePath,
		"--contimeout", "10s", "--timeout", "20s")
	cmd.Env = rcloneEnv()
	cmd.Stdin = strings.NewReader("probe")
	out, err := cmd.CombinedOutput()
	ok := err == nil
	if ok {
		// 探测成功，清理探测文件（115 上传慢，删除也加长超时；失败不阻塞结果）
		del := exec.Command(binPath("rclone"), "deletefile", probePath,
			"--contimeout", "10s", "--timeout", "30s")
		del.Env = rcloneEnv()
		_ = del.Run()
	}
	hint := ""
	if !ok {
		so := string(out)
		switch {
		case strings.Contains(so, "401") || strings.Contains(so, "refresh_token") || strings.Contains(so, "unauthorized"):
			hint = "云盘授权已失效，请到 OpenList 后台重新授权该网盘"
		case strings.Contains(so, "500") || strings.Contains(so, "Internal Server Error"):
			hint = "云盘写入失败（返回 500），通常是授权已失效，请到 OpenList 后台重新授权该网盘"
		default:
			line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(so), "\n", 2)[0])
			if len(line) > 120 {
				line = line[:120] + "…"
			}
			hint = "云盘连接异常：" + line
		}
	}

	authMu.Lock()
	authCache[remote] = authProbe{ok: ok, hint: hint, checked: time.Now()}
	authMu.Unlock()
	return ok, hint
}

var cloudLabelMap = map[string]string{"tianyi": "天翼云盘", "y115": "115"}

// rcloneEnv：rclone 子进程公共环境
func rcloneEnv() []string {
	return append(os.Environ(),
		"RCLONE_CONFIG="+filepath.Join(varDir(), "rclone.conf"),
		"XDG_CACHE_HOME="+filepath.Join(varDir(), "cache"),
	)
}

// rcloneObscure：rclone obscure 加密密码（写 rclone.conf 需要 obscured 格式）
func rcloneObscure(plain string) string {
	cmd := exec.Command(binPath("rclone"), "obscure", plain)
	cmd.Env = rcloneEnv()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// writeRcloneSection：在 rclone.conf 中写入/更新一个远端段落
func writeRcloneSection(name string, kvs [][2]string) error {
	path := filepath.Join(varDir(), "rclone.conf")
	data, _ := os.ReadFile(path)
	var kept, out []string
	inTarget := false
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			inTarget = t == "["+name+"]"
			if inTarget {
				continue // 丢弃旧段落
			}
		}
		if !inTarget {
			kept = append(kept, line)
		}
	}
	out = append(out, "["+name+"]")
	for _, kv := range kvs {
		out = append(out, kv[0]+" = "+kv[1])
	}
	content := strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n\n" + strings.Join(out, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0600)
}

// findOrCreateRepoLocked：按 云盘+目录 查找仓库，不存在则自动创建。
// cloudKey 支持两种：tianyi/y115（ol 中转），或 WebDAV 仓库的远端名 wd-xxxxxxxx。
// password 为空时自动随机生成（旧逻辑）；非空则用传入的密码（新建任务的密码在任务侧生成）。
// 调用方需持有 cfgMu。返回（仓库，是否新建，错误）。
func findOrCreateRepoLocked(cloudKey, subdir, password string) (*Repo, bool, error) {
	sub := strings.Trim(subdir, "/")
	if sub == "" {
		return nil, false, fmt.Errorf("目标目录不能为空")
	}
	var uri, displayName string
	var davSrc *Repo
	if label, ok := cloudLabelMap[cloudKey]; ok {
		uri = "rclone:ol:" + label + "/" + sub
		displayName = label + "·" + sub
	} else {
		// WebDAV：cloudKey 即 rclone 远端名，须已有同名远端的仓库
		for i := range cfg.Repos {
			if cfg.Repos[i].Remote == cloudKey {
				rp := cfg.Repos[i]
				davSrc = &rp
				break
			}
		}
		if davSrc == nil {
			return nil, false, fmt.Errorf("WebDAV 远端不存在: %s", cloudKey)
		}
		uri = "rclone:" + cloudKey + ":" + sub
		displayName = davSrc.Name + "·" + sub
	}
	for i := range cfg.Repos {
		if cfg.Repos[i].URI == uri {
			return &cfg.Repos[i], false, nil
		}
	}
	pw := password
	if pw == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		pw = hex.EncodeToString(b)
	}
	seg := sub
	if idx := strings.LastIndex(seg, "/"); idx >= 0 {
		seg = seg[idx+1:]
	}
	nr := Repo{ID: "repo-" + randomID(), Name: displayName, URI: uri, Password: pw, Cloud: "webdav", Remote: cloudKey}
	if davSrc != nil {
		nr.DavURL, nr.DavUser, nr.DavPass, nr.DavVendor = davSrc.DavURL, davSrc.DavUser, davSrc.DavPass, davSrc.DavVendor
		nr.Name = davSrc.Name + "·" + seg
	}
	cfg.Repos = append(cfg.Repos, nr)
	return &cfg.Repos[len(cfg.Repos)-1], true, nil
}

// initFailHint：读取 init 日志，把失败原因翻译成用户能懂的话
func initFailHint(logPath string) string {
	data, _ := os.ReadFile(logPath)
	s := string(data)
	switch {
	case strings.Contains(s, "config file already exists"):
		return "该云端目录下已存在一个加密仓库（可能是之前创建过的，密码与本仓库不一致）。请换一个云端目录名，或先在「云盘仓库」里删除旧仓库"
	case strings.Contains(s, "wrong password"):
		return "仓库密码不正确"
	case cloudDirNotFound(s):
		// rclone 写入报 404：云端目录不存在（WebDAV 根下没有这个子目录），
		// 需要先创建目录，或改用已存在的目录（如「天翼云盘/飞牛加密备份」）。
		return "云端目录不存在（写入返回 404）。请先在该网盘里创建对应目录，或在「云端目录」里选择已存在的路径（WebDAV 需带一级目录名，如「天翼云盘/飞牛加密备份」）"
	case cloudUploadFailed(s):
		// 列目录正常但上传 500：几乎可以肯定是云盘授权（token/cookie）失效，
		// 而不是网络或密码问题。把根因明确抛给用户，避免只看到笼统的 500。
		return "云盘写入失败（上传返回 500）。通常是云盘授权已失效——请到 OpenList 后台对该网盘重新授权/重新登录，再重试；若刚授权过，也可能是云盘接口临时故障，稍后重试"
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < 2; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" && !strings.HasPrefix(l, "$") && !strings.HasPrefix(l, "[diag]") {
			tail = append([]string{l}, tail...)
		}
	}
	if len(tail) > 0 {
		msg := strings.Join(tail, "；")
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		return msg
	}
	return "exit status 1"
}

// cloudDirNotFound：判断 init 失败是否属于「云端目录不存在（404）」。
// 特征：日志里出现 404 Not Found（rclone 写入目标路径不存在）。
func cloudDirNotFound(s string) bool {
	return strings.Contains(s, "404 Not Found") ||
		strings.Contains(s, "Not Found: 404")
}

// cloudUploadFailed：判断 init 失败是否属于「云盘上传（写入）500」。
// 特征：日志里同时出现「上传错误」与「500」——说明列目录/认证阶段正常，
// 但写入被服务端拒绝，最典型的原因是云盘授权（refresh_token/cookie）失效。
func cloudUploadFailed(s string) bool {
	uploadErr := strings.Contains(s, "unchunked simple update failed") ||
		strings.Contains(s, "Post request put error") ||
		strings.Contains(s, "put error") ||
		strings.Contains(s, "rcat error")
	http500 := strings.Contains(s, "500 Internal Server Error") ||
		strings.Contains(s, "unexpected HTTP response (500)")
	return uploadErr && http500
}

// purgeCloudRepo：用 rclone purge 删除仓库对应的云端目录。
// init 失败回滚时调用，清掉 restic 写了一半的 config/keys/data 半成品，
// 否则下次在同一目录新建仓库会撞「config file already exists」。
// URI 形如 rclone:<remote>:<path>，取其 <remote>:<path> 交给 rclone purge。
// 仅对 rclone 后端生效；失败不阻塞回滚（仅告警日志）。
func purgeCloudRepo(repo *Repo) {
	if repo == nil || !strings.HasPrefix(repo.URI, "rclone:") {
		return
	}
	target := strings.TrimPrefix(repo.URI, "rclone:")
	if target == "" {
		return
	}
	// rclone purge 需要远程在 rclone.conf 里已配置；WebDAV 直连仓库写入了对应 wd- 段，
	// ol 段（OpenList）也常驻，均可用。加超时避免 purge 本身卡死。
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath("rclone"), "purge", target)
	cmd.Env = rcloneEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[warn] 回滚清理云端目录失败 %s: %v: %s", target, err, strings.TrimSpace(string(out)))
	} else {
		log.Printf("回滚已清理云端目录 %s", target)
	}
}

// initRepoTimeout：执行 restic init，带超时。云盘 404/500 时 restic 会无限退避重试
// （30s→1m→2m…），必须加超时避免保存仓库的请求被卡死。stderr 落 logPath 供 initFailHint 诊断。
func initRepoTimeout(repo *Repo, logPath string, timeout time.Duration) runResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath("restic"), "init")
	cmd.Env = append(os.Environ(), resticEnv(repo)...)
	var lf *os.File
	if logPath != "" {
		if f, err := os.Create(logPath); err == nil {
			lf = f
			cmd.Stderr = f
			fmt.Fprintf(f, "$ restic init\n")
		}
	}
	if lf != nil {
		defer lf.Close()
	}
	var stderrBuf bytes.Buffer
	if lf == nil {
		cmd.Stderr = &stderrBuf
	}
	err := cmd.Run()
	res := runResult{Err: err}
	if err != nil {
		res.ErrOutput = err.Error()
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.Err = fmt.Errorf("初始化超时（%v）：云端写入无响应，可能是目录不存在或授权失效", timeout)
		res.ErrOutput = res.Err.Error()
	}
	return res
}

// ---------------------------------------------------------------------------
// restic 执行引擎
// ---------------------------------------------------------------------------

func resticEnv(repo *Repo) []string {
	return []string{
		"RESTIC_REPOSITORY=" + repo.URI,
		"RESTIC_PASSWORD=" + repo.Password,
		"RCLONE_CONFIG=" + filepath.Join(varDir(), "rclone.conf"),
		"XDG_CACHE_HOME=" + filepath.Join(varDir(), "cache"),
		// restic 的 rclone 后端会从 PATH 里拉起 rclone 子进程
		"PATH=" + filepath.Dir(binPath("rclone")) + ":" + os.Getenv("PATH"),
	}
}

type resticStatus struct {
	MessageType         string   `json:"message_type"`
	PercentDone         float64  `json:"percent_done"`
	TotalFiles          int64    `json:"total_files"`
	FilesDone           int64    `json:"files_done"`
	TotalBytes          int64    `json:"total_bytes"`
	BytesDone           int64    `json:"bytes_done"`
	CurrentFiles        []string `json:"current_files"`
	ErrorCount          int64    `json:"error_count"`
	SnapshotID          string   `json:"snapshot_id"`
	TotalFilesProcessed int64    `json:"total_files_processed"`
	TotalBytesProcessed int64    `json:"total_bytes_processed"`
	SecondsElapsed      float64  `json:"seconds_elapsed"`
	TotalDuration       float64  `json:"total_duration"`
	BackupStart         string   `json:"backup_start"`
	BackupEnd           string   `json:"backup_end"`
	FilesNew            int64    `json:"files_new"`
	FilesChanged        int64    `json:"files_changed"`
	FilesUnmodified     int64    `json:"files_unmodified"`
	Code                int64    `json:"code"`
	Message             string   `json:"message"`
}

type runResult struct {
	SnapshotID string
	Files      int64
	Bytes      int64
	Err        error
	ErrOutput  string
	Stopped    bool // 手动停止（区别于真实失败）
}

// runRestic 执行一条 restic 命令，逐行解析 --json 输出更新进度，日志写入 writer。
func runRestic(repo *Repo, args []string, logFile *os.File, stop chan struct{}, parseProgress bool) runResult {
	cmd := exec.Command(binPath("restic"), args...)
	cmd.Env = append(os.Environ(), resticEnv(repo)...)
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = logFile
	fmt.Fprintf(logFile, "\n$ restic %s\n", strings.Join(args, " "))
	// TODO: 临时诊断日志，定位 verify 密码问题后删除
	{
		bp := binPath("restic")
		pwLen := len(repo.Password)
		_, statErr := os.Stat(bp)
		rcPath := filepath.Join(varDir(), "rclone.conf")
		_, rcErr := os.Stat(rcPath)
		fmt.Fprintf(logFile, "[diag] bin=%s stat_ok=%v pw_len=%d uri=%q rclone_conf_ok=%v\n",
			bp, statErr == nil, pwLen, repo.URI, rcErr == nil)
	}
	_ = cmd.Start()

	res := runResult{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			fmt.Fprintln(logFile, line)
			if !parseProgress {
				continue
			}
			var st resticStatus
			if err := json.Unmarshal([]byte(line), &st); err != nil {
				continue
			}
			stateMu.Lock()
			switch st.MessageType {
			case "status":
				runPct = st.PercentDone * 100
				runFiles = st.FilesDone
				runBytes = st.BytesDone
				runTotal = st.TotalBytes
				if len(st.CurrentFiles) > 0 {
					runCur = st.CurrentFiles
				}
				runErrs = st.ErrorCount
			case "summary":
				res.SnapshotID = st.SnapshotID
				res.Files = st.TotalFilesProcessed
				res.Bytes = st.TotalBytesProcessed
			}
			stateMu.Unlock()
		}
	}()

	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()

	select {
	case err := <-finished:
		<-done
		res.Err = err
		if res.Err != nil {
			res.ErrOutput = res.Err.Error()
		}
		return res
	case <-stop:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-finished
		<-done
		res.Err = fmt.Errorf("已手动停止")
		res.Stopped = true
		return res
	}
}

// ---------------------------------------------------------------------------
// 任务运行
// ---------------------------------------------------------------------------

func logDir() string { return filepath.Join(varDir(), "logs") }

func runTask(t *Task) error {
	repo := findRepo(t.RepoID)
	if repo == nil {
		return fmt.Errorf("仓库 %s 不存在", t.RepoID)
	}

	// 备份前探测云盘授权：失效时直接明确报错，避免 restic 产生一串 500 重试日志
	if ok, hint := probeCloudAuth(repo); !ok {
		cfgMu.Lock()
		if lt := findTask(t.ID); lt != nil {
			lt.LastStatus = "failed"
			lt.LastRunAt = time.Now().Format("2006-01-02 15:04:05")
			lt.LastMessage = hint
		}
		_ = saveConfig()
		cfgMu.Unlock()
		return fmt.Errorf("%s", hint)
	}

	stateMu.Lock()
	if running != "" {
		stateMu.Unlock()
		return fmt.Errorf("已有任务在运行中，请稍后再试")
	}
	running = t.ID
	runStop = make(chan struct{})
	runStart = time.Now().Format(time.RFC3339)
	runPct, runFiles, runBytes, runTotal, runErrs = 0, 0, 0, 0, 0
	runCur = nil
	ts := time.Now().Format("2006-01-02_150405")
	_ = os.MkdirAll(filepath.Join(logDir(), t.ID), 0o755)
	lf, _ := os.Create(filepath.Join(logDir(), t.ID, ts+".log"))
	runLog = filepath.Join(logDir(), t.ID, ts+".log")
	stateMu.Unlock()
	defer lf.Close()

	defer func() {
		stateMu.Lock()
		running = ""
		runStop = nil
		runStart = ""
		runCur = nil
		stateMu.Unlock()
	}()

	// 更新任务状态：运行中（用 cfg 内活跃指针，调用方传入的可能是副本）
	cfgMu.Lock()
	if lt := findTask(t.ID); lt != nil {
		lt.LastRunAt = time.Now().Format("2006-01-02 15:04:05")
		lt.LastStatus = "running"
		lt.LastMessage = ""
	}
	_ = saveConfig()
	cfgMu.Unlock()

	start := time.Now()

	// 前置：仓库尚未初始化时先补 init（并发建任务的竞态兑底：
	// 任务 A 的 init 还在走云盘网络时，任务 B/运行请求可能已经先到）
	if !repo.Inited {
		ilogPath := filepath.Join(varDir(), "init-"+repo.ID+".log")
		ilf, _ := os.Create(ilogPath)
		ires := runRestic(repo, []string{"init"}, ilf, nil, false)
		ilf.Close()
		// init 成功，或远端本来就有仓库（config 已存在，日志在 stderr→ilogPath）都算就绪
		logData, _ := os.ReadFile(ilogPath)
		logStr := string(logData)
		ok := ires.Err == nil || strings.Contains(logStr, "config file already exists")
		cfgMu.Lock()
		if lr := findRepo(repo.ID); lr != nil {
			lr.Inited = true
		}
		_ = saveConfig()
		cfgMu.Unlock()
		if !ok {
			hint := initFailHint(ilogPath)
			cfgMu.Lock()
			if lt := findTask(t.ID); lt != nil {
				lt.LastStatus = "failed"
				lt.LastMessage = "仓库初始化失败：" + hint
			}
			_ = saveConfig()
			cfgMu.Unlock()
			fmt.Fprintf(lf, "[error] 仓库初始化失败: %s\n", hint)
			return fmt.Errorf("仓库初始化失败: %s", hint)
		}
	}

	// 前置：静默 unlock 清理陈旧锁
	ul := runRestic(repo, []string{"unlock"}, lf, nil, false)
	if ul.Err != nil {
		fmt.Fprintf(lf, "[warn] unlock 失败（忽略）: %v\n", ul.Err)
	}

	// 备份
	args := []string{"backup", "--json"}
	// 打任务标签：仓库 Tab 靠它精确归属快照（历史快照没有 tag，读取时按路径兜底）
	if t.ID != "" {
		args = append(args, "--tag", "task:"+t.ID)
	}
	args = append(args, t.Paths...)
	for _, ex := range t.Excludes {
		if ex != "" {
			args = append(args, "--exclude", ex)
		}
	}
	res := runRestic(repo, args, lf, runStop, true)
	if res.Err != nil {
		// 锁冲突时自动 unlock 重试一次
		if strings.Contains(res.ErrOutput, "already locked") || strings.Contains(res.ErrOutput, "unable to create lock") {
			_ = runRestic(repo, []string{"unlock"}, lf, nil, false)
			res = runRestic(repo, args, lf, runStop, true)
		}
	}
	dur := int(time.Since(start).Seconds())

	cfgMu.Lock()
	lt := findTask(t.ID)
	if lt == nil {
		cfgMu.Unlock()
		if res.Err != nil {
			return res.Err
		}
		return nil
	}
	if res.Stopped {
		lt.LastStatus = "stopped"
		lt.LastMessage = "已手动停止"
	} else if res.Err != nil {
		lt.LastStatus = "failed"
		lt.LastMessage = res.Err.Error()
	} else {
		lt.LastStatus = "success"
		lt.LastMessage = fmt.Sprintf("新增快照 %s：%d 个文件 / %s", shortID(res.SnapshotID), res.Files, humanBytes(res.Bytes))
		lt.LastDoneAt = time.Now().Format("2006-01-02 15:04:05")
		lt.LastFiles = res.Files
		lt.LastBytes = res.Bytes
		lt.LastSnapID = res.SnapshotID
		lt.LastDuration = dur

		// 保留策略
		if lt.KeepLast > 0 {
			fr := runRestic(repo, []string{"forget", "--group-by", "paths", "--keep-last", strconv.Itoa(lt.KeepLast), "--prune"}, lf, nil, false)
			if fr.Err != nil {
				fmt.Fprintf(lf, "[warn] forget 失败: %v\n", fr.Err)
			}
		}
	}
	_ = saveConfig()
	cfgMu.Unlock()

	stateMu.Lock()
	runLog = ""
	stateMu.Unlock()

	if res.Err != nil {
		return res.Err
	}
	// 备份成功（含 forget/prune 之后）异步刷新快照缓存，保证还原页时间线与云盘一致
	go func(rp Repo) { refreshSnapCache(&rp) }(*repo)
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ---------------------------------------------------------------------------
// 调度器
// ---------------------------------------------------------------------------

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// schedHM：解析 "03:00" 为时/分
func schedHM(s string) (int, int, bool) {
	hm := strings.SplitN(s, ":", 2)
	if len(hm) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(hm[0])
	m, err2 := strconv.Atoi(hm[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// nextRunAt 计算任务的下一次运行时间（仅用于展示，不影响实际调度）。
// 返回空字符串表示无计划（manual / 已停用）。
func nextRunAt(t *Task, now time.Time) string {
	if !t.Enabled || t.SchedMode == "manual" {
		return ""
	}
	switch t.SchedMode {
	case "daily":
		h, m, ok := schedHM(t.DailyTime)
		if !ok {
			return ""
		}
		n := t.DailyDays
		if n < 1 {
			n = 1
		}
		cand := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.Local)
		for !cand.After(now) {
			cand = cand.AddDate(0, 0, n)
		}
		return cand.Format("2006-01-02 15:04:05")
	case "weekly":
		h, m, ok := schedHM(t.DailyTime)
		if !ok || len(t.WeeklyDays) == 0 {
			return ""
		}
		for d := 0; d <= 7; d++ {
			cand := time.Date(now.Year(), now.Month(), now.Day()+d, h, m, 0, 0, time.Local)
			if cand.After(now) && containsInt(t.WeeklyDays, int(cand.Weekday())) {
				return cand.Format("2006-01-02 15:04:05")
			}
		}
		return ""
	case "monthly":
		h, m, ok := schedHM(t.DailyTime)
		if !ok || len(t.MonthlyDays) == 0 {
			return ""
		}
		// 最多向后找 62 天，覆盖所有月份组合
		for d := 0; d <= 62; d++ {
			cand := time.Date(now.Year(), now.Month(), now.Day()+d, h, m, 0, 0, time.Local)
			day := cand.Day()
			lastDay := time.Date(cand.Year(), cand.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
			if cand.After(now) && (containsInt(t.MonthlyDays, day) || (day == lastDay && containsInt(t.MonthlyDays, -1))) {
				return cand.Format("2006-01-02 15:04:05")
			}
		}
		return ""
	case "interval":
		if t.IntervalHrs <= 0 {
			return ""
		}
		lastDone, _ := time.ParseInLocation("2006-01-02 15:04:05", t.LastDoneAt, time.Local)
		base := lastDone
		if base.IsZero() {
			base = now
		}
		next := base.Add(time.Duration(t.IntervalHrs) * time.Hour)
		for !next.After(now) {
			next = next.Add(time.Duration(t.IntervalHrs) * time.Hour)
		}
		return next.Format("2006-01-02 15:04:05")
	}
	return ""
}

func scheduler() {
	for {
		time.Sleep(30 * time.Second)
		cfgMu.Lock()
		now := time.Now()
		var due []*Task
		for i := range cfg.Tasks {
			t := &cfg.Tasks[i]
			if !t.Enabled || t.SchedMode == "manual" || t.LastStatus == "running" {
				continue
			}
			lastDone, _ := time.ParseInLocation("2006-01-02 15:04:05", t.LastDoneAt, time.Local)
			lastTry, _ := time.ParseInLocation("2006-01-02 15:04:05", t.LastRunAt, time.Local)
			switch t.SchedMode {
			case "daily":
				h, m, ok := schedHM(t.DailyTime)
				if !ok {
					continue
				}
				n := t.DailyDays
				if n < 1 {
					n = 1
				}
				runTime := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.Local)
				// lastTry < runTime：本周期尚未尝试过（含手动停止/失败）才补跑，避免停止后每 30s 重启
				if now.After(runTime) && lastDone.Before(runTime) && lastTry.Before(runTime) &&
					(lastDone.IsZero() || int(now.Sub(lastDone).Hours()/24) >= n) {
					due = append(due, t)
				}
			case "weekly":
				h, m, ok := schedHM(t.DailyTime)
				if !ok || len(t.WeeklyDays) == 0 {
					continue
				}
				runTime := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.Local)
				if now.After(runTime) && lastDone.Before(runTime) && lastTry.Before(runTime) && containsInt(t.WeeklyDays, int(now.Weekday())) {
					due = append(due, t)
				}
			case "monthly":
				h, m, ok := schedHM(t.DailyTime)
				if !ok || len(t.MonthlyDays) == 0 {
					continue
				}
				runTime := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.Local)
				lastDay := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
				day := now.Day()
				if containsInt(t.MonthlyDays, day) ||
					(day == lastDay && containsInt(t.MonthlyDays, -1)) {
					if now.After(runTime) && lastDone.Before(runTime) && lastTry.Before(runTime) {
						due = append(due, t)
					}
				}
			case "interval":
				if t.IntervalHrs <= 0 {
					continue
				}
				if lastDone.IsZero() {
					// 从未成功过：以上次尝试时间为准，仍无则立即触发
					lastTry, _ := time.ParseInLocation("2006-01-02 15:04:05", t.LastRunAt, time.Local)
					if lastTry.IsZero() || now.Sub(lastTry) >= time.Duration(t.IntervalHrs)*time.Hour {
						due = append(due, t)
					}
				} else if now.Sub(lastDone) >= time.Duration(t.IntervalHrs)*time.Hour {
					due = append(due, t)
				}
			}
		}
		cfgMu.Unlock()

		// 调度循环顺带做云盘授权探测：为每个 enabled 任务的 ol 仓库探测一次，
		// 把失效告警写入 Task.AuthWarn，前端任务卡片可直接标红（无需等备份失败）。
		go refreshAuthWarns()

		for _, t := range due {
			go func(task *Task) {
				_ = runTask(task)
			}(t)
			time.Sleep(2 * time.Second) // 保证串行（runTask 内部有 running 互斥）
		}
	}
}

// refreshAuthWarns：遍历 enabled 任务的仓库，探测云盘授权（结果只写 authCache，
// 由 configHandler 返回时填充到 Task.AuthWarn，避免污染落盘的 cfg）。
func refreshAuthWarns() {
	cfgMu.Lock()
	var repoIDs []string
	seen := map[string]bool{}
	for i := range cfg.Tasks {
		t := &cfg.Tasks[i]
		if !t.Enabled || t.SchedMode == "manual" {
			continue
		}
		if !seen[t.RepoID] {
			seen[t.RepoID] = true
			repoIDs = append(repoIDs, t.RepoID)
		}
	}
	cfgMu.Unlock()

	for _, rid := range repoIDs {
		cfgMu.Lock()
		repo := findRepo(rid)
		cfgMu.Unlock()
		if repo == nil {
			continue
		}
		_, _ = probeCloudAuth(repo) // 结果进 authCache
	}
}

// ---------------------------------------------------------------------------
// API
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

type progressInfo struct {
	TaskID    string   `json:"task_id"`
	StartedAt string   `json:"started_at"`
	Percent   float64  `json:"percent"`
	Files     int64    `json:"files_done"`
	Bytes     int64    `json:"bytes_done"`
	Total     int64    `json:"total_bytes"`
	Current   []string `json:"current_files"`
	Errors    int64    `json:"error_count"`
	LogFile   string   `json:"log_file"`
}

func progressHandler(w http.ResponseWriter, r *http.Request) {
	stateMu.Lock()
	p := progressInfo{TaskID: running, StartedAt: runStart, Percent: runPct, Files: runFiles, Bytes: runBytes,
		Total: runTotal, Current: runCur, Errors: runErrs, LogFile: runLog}
	stateMu.Unlock()
	writeJSON(w, 200, p)
}

func statusHandler(gateway string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stateMu.Lock()
		runID := running
		stateMu.Unlock()
		sc := newScope(r)
		home := ""
		if hs := homeDirsFor(sc.ID.UID); len(hs) > 0 {
			home = hs[0]
		}
		writeJSON(w, 200, map[string]any{
			"app":         "encbackup",
			"version":     appVersion,
			"gateway":     gateway,
			"socket_mode": socketMode(),
			"data_dir":    varDir(),
			"user_root":   sc.Display[0],
			"roots":       sc.Display,
			"home":        home,
			"auth": map[string]any{
				"uid":        sc.ID.UID,
				"name":       sc.ID.Name,
				"admin":      sc.ID.Admin,
				"identified": sc.ID.OK,
				"source":     sc.Source,
			},
			"restic_ready":   engineVersion("restic") != "",
			"restic_version": engineVersion("restic"),
			"rclone_version": engineVersion("rclone"),
			"running_task":   runID,
		})
	}
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if r.Method == http.MethodGet {
		// 深拷贝后掩码，绝不修改内存 cfg（否则掩码会被 saveConfig 落盘污染密码）
		cp := *cfg
		cp.Repos = make([]Repo, len(cfg.Repos))
		for i, rp := range cfg.Repos {
			cp.Repos[i] = rp
			if cp.Repos[i].Password != "" {
				cp.Repos[i].Password = "******"
			}
			if cp.Repos[i].DavPass != "" {
				cp.Repos[i].DavPass = "******"
			}
		}
		cp.Tasks = append([]Task(nil), cfg.Tasks...)
		now := time.Now()
		for i := range cp.Tasks {
			cp.Tasks[i].NextRunAt = nextRunAt(&cfg.Tasks[i], now)
			// 任务侧密码掩码（实际密码存于对应 repo，任务字段仅作回显占位，不泄露明文）
			if cp.Tasks[i].Password != "" {
				cp.Tasks[i].Password = "******"
			}
			// 填充云盘授权告警：从 authCache 读该任务仓库的探测结论
			if rp := findRepo(cp.Tasks[i].RepoID); rp != nil {
				if rem := olRemote(rp); rem != "" {
					authMu.Lock()
					if p, ok := authCache[rem]; ok && !p.ok {
						cp.Tasks[i].AuthWarn = p.hint
					}
					authMu.Unlock()
				}
			}
		}
		writeJSON(w, 200, &cp)
		return
	}
	writeJSON(w, 405, map[string]string{"error": "method not allowed"})
}

// reposHandler：POST 新建 / PUT 更新 / DELETE 删除
func reposHandler(w http.ResponseWriter, r *http.Request) {
	id := afterMarker(r.URL.Path, "/api/repos/")
	switch r.Method {
	case http.MethodPost:
		var nr Repo
		if err := readBody(r, &nr); err != nil {
			writeJSON(w, 400, map[string]string{"error": "参数错误"})
			return
		}
		// WebDAV 直连：校验必填项、生成远端名、写 rclone.conf、拼 URI
		if nr.Cloud == "webdav" {
			nr.DavURL = strings.TrimRight(strings.TrimSpace(nr.DavURL), "/")
			if nr.DavURL == "" || nr.DavUser == "" {
				writeJSON(w, 400, map[string]string{"error": "WebDAV 地址与账号必填"})
				return
			}
			cfgMu.Lock()
			if old := findRepo(nr.ID); old != nil {
				nr = unmaskPassword(nr, old)
				if nr.Remote == "" {
					nr.Remote = old.Remote
				}
			}
			cfgMu.Unlock()
			if nr.Remote == "" {
				nr.Remote = "wd-" + randomID()
			}
			if nr.DavPass != "" && nr.DavPass != "******" {
				if ob := rcloneObscure(nr.DavPass); ob != "" {
					nr.DavPass = ob
				}
			}
			vendor := nr.DavVendor
			if vendor == "" {
				vendor = "other"
			}
			// 地址缺协议头时自动补 http://（rclone 无法解析无 scheme 的 URL）
			nr.DavURL = strings.TrimSpace(nr.DavURL)
			if nr.DavURL != "" && !strings.Contains(nr.DavURL, "://") {
				nr.DavURL = "http://" + nr.DavURL
			}
			err := writeRcloneSection(nr.Remote, [][2]string{
				{"type", "webdav"}, {"url", nr.DavURL}, {"vendor", vendor},
				{"user", nr.DavUser}, {"pass", nr.DavPass},
			})
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": "写入 rclone 配置失败: " + err.Error()})
				return
			}
			sub := strings.Trim(nr.URI, "/") // 前端把子目录暂存在 URI 字段
			nr.URI = "rclone:" + nr.Remote + ":" + sub
			if nr.Name == "" {
				if sub == "" {
					nr.Name = "WebDAV 连接" // 纯连接（无目录）：友好默认名
				} else {
					nr.Name = "WebDAV·" + sub
				}
			}
			// 纯连接（sub 为空）标记：不参与 restic init / 密码生成，只作为云盘连接载体
			if sub == "" {
				nr.Inited = true // 连接本身无需 init，避免 runTask 误触发
			}
		}
		if nr.URI == "" {
			writeJSON(w, 400, map[string]string{"error": "参数不完整"})
			return
		}
		isNew := false
		isConn := strings.HasSuffix(strings.TrimSpace(nr.URI), ":") // URI 以冒号结尾 = 无子目录 = 纯连接
		cfgMu.Lock()
		if nr.ID == "" {
			nr.ID = "repo-" + randomID()
		}
		if old := findRepo(nr.ID); old != nil {
			nr = unmaskPassword(nr, old)
			*old = nr
		} else {
			isNew = true
			// restic 仓库密码：新建未提供则自动生成（API/Docker 部署友好）；
			// 纯连接（无目录）不生成密码，密码由后续任务创建时生成
			if nr.Password == "" && !isConn {
				b := make([]byte, 16)
				_, _ = rand.Read(b)
				nr.Password = hex.EncodeToString(b)
			}
			cfg.Repos = append(cfg.Repos, nr)
		}
		err := saveConfig()
		cfgMu.Unlock()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		// 新仓库自动 restic init（目录冲突/云盘不可写时回滚并报错）
		// 用带超时的执行，避免云盘 404/500 时 restic 无限退避重试卡死保存请求。
		// 纯连接（无目录）跳过 init——它只是云盘连接，仓库在任务创建时才真正初始化。
		if isNew && !isConn {
			logPath := filepath.Join(varDir(), "init-"+nr.ID+".log")
			ires := initRepoTimeout(&nr, logPath, 180*time.Second)
			if ires.Err != nil {
				hint := initFailHint(logPath)
				// init 失败回滚：删除刚创建的仓库（本地记录）
				_ = deleteRepoByID(nr.ID)
				if strings.Contains(hint, "已存在一个加密仓库") {
					// 目录冲突：云端目录里已有一个仓库（可能是旧仓库/密码不一致），
					// 此时不 purge，避免误删已有数据，仅提示用户换目录
					writeJSON(w, 400, map[string]string{"error": hint})
					return
				}
				// 非冲突类失败（404 目录不存在 / 500 上传失败 / 超时）：
				// restic 可能已写了一半 config/keys 半成品，purge 清掉避免下次撞冲突
				purgeCloudRepo(&nr)
				writeJSON(w, 400, map[string]string{"error": "仓库初始化失败：" + hint})
				return
			}
			cfgMu.Lock()
			if lr := findRepo(nr.ID); lr != nil {
				lr.Inited = true
			}
			_ = saveConfig()
			cfgMu.Unlock()
		}
		writeJSON(w, 200, map[string]string{"id": nr.ID})
	case http.MethodDelete:
		writeJSON(w, 200, map[string]string{"ok": "true", "err": errStr(deleteRepoByID(id))})
	default:
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
	}
}

// deleteRepoByID：删除仓库（DELETE /api/repos/{id} 与 POST /api/repos/{id}/delete 共用）
// 若为「空壳/残缺」仓库（无快照，多为 init 失败残留的半成品），连带清理云端目录，
// 否则下次在同目录新建会撞「config file already exists」。
// 有真实快照数据的仓库只删本地记录、保留云盘数据（与前端「不删云盘数据」承诺一致）。
func deleteRepoByID(id string) error {
	if id == "" {
		return fmt.Errorf("缺少 id")
	}
	// 先取出待删仓库的副本，判断是否为空壳，再删本地记录。
	var target Repo
	found := false
	cfgMu.Lock()
	for i := range cfg.Repos {
		if cfg.Repos[i].ID == id {
			target = cfg.Repos[i]
			cfg.Repos = append(cfg.Repos[:i], cfg.Repos[i+1:]...)
			found = true
			break
		}
	}
	saveErr := saveConfig()
	cfgMu.Unlock()

	if !found {
		return saveErr
	}
	// 空壳判断：restic snapshots 为空（无备份数据）→ 视为残留半成品，purge 云端。
	// 独立于锁外执行，rclone purge / restic snapshots 可能耗时较长，避免阻塞其他请求。
	if repoHasNoSnapshots(&target) {
		purgeCloudRepo(&target)
	}
	return saveErr
}

// repoHasNoSnapshots：判断仓库是否无任何快照（空壳/残缺）。查不到仓库或出错时保守返回 false（不 purge）。
func repoHasNoSnapshots(repo *Repo) bool {
	if repo == nil || !strings.HasPrefix(repo.URI, "rclone:") {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath("restic"), "snapshots", "--json")
	cmd.Env = append(os.Environ(), resticEnv(repo)...)
	out, err := cmd.Output()
	if err != nil {
		// 读不到仓库（密码错/目录缺失/超时）→ 保守不 purge，避免误删
		return false
	}
	var snaps []map[string]interface{}
	if json.Unmarshal(out, &snaps) != nil {
		return false
	}
	return len(snaps) == 0
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// afterMarker：取路径中 marker 之后的部分（兼容网关前缀 /app/encbackup）
func afterMarker(path, marker string) string {
	if i := strings.Index(path, marker); i >= 0 {
		return path[i+len(marker):]
	}
	return ""
}

// repoVerifyHandler：restic snapshots 验证仓库可用
func repoVerifyHandler(w http.ResponseWriter, r *http.Request) {
	id := afterMarker(r.URL.Path, "/api/repos/")
	id = strings.TrimSuffix(id, "/verify")
	repo := func() *Repo {
		cfgMu.Lock()
		defer cfgMu.Unlock()
		src := findRepo(id)
		if src == nil {
			return nil
		}
		cp := *src
		return &cp
	}()
	if repo == nil {
		writeJSON(w, 404, map[string]string{"error": "仓库不存在"})
		return
	}
	lf, _ := os.Create(filepath.Join(varDir(), "verify-"+id+".log"))
	defer lf.Close()
	res := runRestic(repo, []string{"snapshots", "--json"}, lf, nil, false)
	if res.Err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": res.Err.Error()})
		return
	}
	// 解析快照数量
	data, _ := os.ReadFile(lf.Name())
	n := strings.Count(string(data), "\"id\"")
	writeJSON(w, 200, map[string]any{"ok": true, "snapshots": n})
}

// tasksHandler：POST 新建/更新 / DELETE 删除
func tasksHandler(w http.ResponseWriter, r *http.Request) {
	id := afterMarker(r.URL.Path, "/api/tasks/")
	switch r.Method {
	case http.MethodPost:
		var nt Task
		if err := readBody(r, &nt); err != nil {
			writeJSON(w, 400, map[string]string{"error": "参数错误"})
			return
		}
		// 目的地两步式：cloud+subdir → 查找或自动创建仓库
		var newRepo *Repo
		if nt.DestCloud != "" {
			cfgMu.Lock()
			rp, created, ferr := findOrCreateRepoLocked(nt.DestCloud, nt.DestSubdir, nt.Password)
			if ferr != nil {
				cfgMu.Unlock()
				writeJSON(w, 400, map[string]string{"error": ferr.Error()})
				return
			}
			if created {
				cp := *rp
				newRepo = &cp
			}
			nt.RepoID = rp.ID
			_ = saveConfig()
			cfgMu.Unlock()
		}
		if nt.Name == "" || len(nt.Paths) == 0 || nt.RepoID == "" {
			writeJSON(w, 400, map[string]string{"error": "参数不完整：需要 name、paths、目的地（云盘+目录）"})
			return
		}
		// 安全校验：备份源路径必须落在用户可见根目录内（防路径穿越）
		for i, p := range nt.Paths {
			sp, perr := resolveSafePath(r, p)
			if perr != nil {
				writeJSON(w, 400, map[string]string{"error": "备份路径不合法：" + perr.Error()})
				return
			}
			nt.Paths[i] = sp
		}
		// 新建仓库时自动 restic init（失败不阻塞建任务，首次备份会再报）
		// 用带超时的执行，避免 115 等慢写入云盘 init 卡在 create key 无限等待
		initErr := ""
		if newRepo != nil {
			logPath := filepath.Join(varDir(), "init-"+newRepo.ID+".log")
			ires := initRepoTimeout(newRepo, logPath, 180*time.Second)
			if ires.Err != nil {
				hint := initFailHint(logPath)
				if strings.Contains(hint, "已存在一个加密仓库") {
					// 目录冲突：云端目录里已有仓库，不 purge（避免误删已有数据），
					// 只回滚刚建的本地记录，让用户重新选目录
					_ = deleteRepoByID(newRepo.ID)
					writeJSON(w, 400, map[string]string{"error": hint})
					return
				}
				initErr = hint
			} else {
				cfgMu.Lock()
				if lr := findRepo(newRepo.ID); lr != nil {
					lr.Inited = true
				}
				_ = saveConfig()
				cfgMu.Unlock()
			}
		}
		if nt.SchedMode == "" {
			nt.SchedMode = "manual"
		}
		cfgMu.Lock()
		if nt.ID != "" {
			if old := findTask(nt.ID); old != nil {
				created := old.CreatedAt
				lastDone := old.LastDoneAt
				*old = nt
				old.CreatedAt = created
				old.LastDoneAt = lastDone
			} else {
				nt.ID = "task-" + randomID()
				nt.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
				cfg.Tasks = append(cfg.Tasks, nt)
			}
		} else {
			nt.ID = "task-" + randomID()
			nt.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
			cfg.Tasks = append(cfg.Tasks, nt)
		}
		saved := findTask(nt.ID)
		err := saveConfig()
		result := map[string]any{"id": nt.ID}
		if initErr != "" {
			result["init_error"] = initErr
		}
		if saved != nil {
			result["task"] = *saved
		}
		cfgMu.Unlock()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, result)
	case http.MethodDelete:
		deleteTaskByID(w, id)
	default:
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
	}
}

// deleteTaskByID：删除任务（DELETE /api/tasks/{id} 与 POST /api/tasks/{id}/delete 共用）
func deleteTaskByID(w http.ResponseWriter, id string) {
	if id == "" {
		writeJSON(w, 400, map[string]string{"error": "缺少 id"})
		return
	}
	stateMu.Lock()
	isRun := running == id
	stateMu.Unlock()
	if isRun {
		writeJSON(w, 409, map[string]string{"error": "任务正在运行，请先停止"})
		return
	}
	cfgMu.Lock()
	for i := range cfg.Tasks {
		if cfg.Tasks[i].ID == id {
			cfg.Tasks = append(cfg.Tasks[:i], cfg.Tasks[i+1:]...)
			break
		}
	}
	err := saveConfig()
	cfgMu.Unlock()
	writeJSON(w, 200, map[string]string{"ok": "true", "err": errStr(err)})
}

// taskDeleteHandler：POST /api/tasks/{id}/delete —— 网关对 DELETE 方法转发兼容性差，删除操作改走 POST
func taskDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/tasks/"), "/delete")
	deleteTaskByID(w, id)
}

func taskRunHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/tasks/"), "/run")
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	cfgMu.Lock()
	t := findTask(id)
	var cp *Task
	if t != nil {
		c := *t
		cp = &c
	}
	cfgMu.Unlock()
	if cp == nil {
		writeJSON(w, 404, map[string]string{"error": "任务不存在"})
		return
	}
	stateMu.Lock()
	busy := running != ""
	stateMu.Unlock()
	if busy {
		writeJSON(w, 409, map[string]string{"error": "已有任务在运行中"})
		return
	}
	go func() { _ = runTask(cp) }()
	writeJSON(w, 200, map[string]string{"ok": "true"})
}

func taskStopHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/tasks/"), "/stop")
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	if running != id || runStop == nil {
		writeJSON(w, 409, map[string]string{"error": "该任务当前未在运行"})
		return
	}
	close(runStop)
	writeJSON(w, 200, map[string]string{"ok": "true"})
}

func taskRunsHandler(w http.ResponseWriter, r *http.Request) {
	// /api/tasks/{id}/runs
	p := afterMarker(r.URL.Path, "/api/tasks/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 2 || parts[1] != "runs" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	taskID := parts[0]
	dir := filepath.Join(logDir(), taskID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, 200, []string{})
		return
	}
	var runs []map[string]string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		runs = append(runs, map[string]string{
			"file": e.Name(),
			"size": strconv.FormatInt(info.Size(), 10),
		})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i]["file"] > runs[j]["file"] })
	writeJSON(w, 200, runs)
}

func taskLogHandler(w http.ResponseWriter, r *http.Request) {
	// /api/tasks/{id}/log?file=xxx
	p := afterMarker(r.URL.Path, "/api/tasks/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 2 || parts[1] != "log" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	file := r.URL.Query().Get("file")
	taskID := parts[0]
	// 防目录穿越
	file = filepath.Base(file)
	fp := filepath.Join(logDir(), taskID, file)
	if fp == filepath.Join(logDir(), taskID, ".") || file == "." || file == "/" {
		writeJSON(w, 400, map[string]string{"error": "bad file"})
		return
	}
	// 正在运行且未指定文件 → 读当前日志
	stateMu.Lock()
	cur := runLog
	curID := running
	stateMu.Unlock()
	if file == "." && curID == taskID && cur != "" {
		fp = cur
	} else if file == "." {
		// 默认取最新一份
		entries, _ := os.ReadDir(filepath.Join(logDir(), taskID))
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
		if len(entries) == 0 {
			writeJSON(w, 200, map[string]string{"content": ""})
			return
		}
		fp = filepath.Join(logDir(), taskID, entries[0].Name())
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "日志不存在"})
		return
	}
	// 只回尾部 32KB
	if len(data) > 32*1024 {
		data = data[len(data)-32*1024:]
	}
	writeJSON(w, 200, map[string]string{"content": string(data), "file": file})
}

// logNode：日志时间轴上的一个关键节点
type logNode struct {
	Time string `json:"time"` // HH:MM
	Type string `json:"type"` // start | upload | warn | done | fail
	Msg  string `json:"msg"`
}

// logSummary：精简日志视图的结构化结果
type logSummary struct {
	Status    string    `json:"status"`     // success | failed | running
	Title     string    `json:"title"`      // 如 "10-26 17:00 的备份"
	OneLine   string    `json:"one_line"`   // 一行结果："12,846 个文件 · 13.5 GB · 用时 18 分"
	Percent   float64   `json:"percent"`    // 运行中时的进度（0-100）
	Speed     string    `json:"speed"`      // 运行中时的速度（可选）
	Nodes     []logNode `json:"nodes"`      // 关键节点时间轴
	ErrDetail string    `json:"err_detail"` // 失败时的原始错误详情（可折叠）
}

// parseLogSummary：解析 restic 日志文件，提取精简结果 + 关键节点。
// fileTime 为日志文件名里携带的时间戳（失败且无 summary 时兜底用）。
func parseLogSummary(data []byte, running bool, fileTime time.Time) logSummary {
	s := logSummary{Status: "success", Nodes: []logNode{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)

	var (
		backupStart    time.Time
		backupEnd      time.Time
		totalFiles     int64
		totalBytes     int64
		totalDur       float64
		snapID         string
		firstElapsed   float64 // 第一条 status 的 seconds_elapsed（相对开始）
		uploadElapsed  float64 // files_done 首次 >0 时的 seconds_elapsed
		hasFirstStatus bool
		hasUpload      bool
		warnSeen       bool
		warnCount      int64
		exitMsg        string
		lastPct        float64
		lastSpeed      float64
		lastBytes      int64
	)
	hasSummary := false

	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "$") || strings.HasPrefix(trimmed, "[diag]") {
			continue
		}
		var st resticStatus
		if err := json.Unmarshal([]byte(line), &st); err != nil {
			// 非 JSON 行（如 "Fatal: ..." 纯文本错误）
			if strings.Contains(trimmed, "Fatal:") && exitMsg == "" {
				exitMsg = trimmed
			}
			continue
		}
		switch st.MessageType {
		case "status":
			lastPct = st.PercentDone
			lastBytes = st.BytesDone
			if totalFiles == 0 {
				totalFiles = st.TotalFiles
			}
			if st.TotalBytes > 0 {
				totalBytes = st.TotalBytes
			}
			// 估算速度：bytes_done / seconds_elapsed
			if st.SecondsElapsed > 0 && st.BytesDone > 0 {
				lastSpeed = float64(st.BytesDone) / st.SecondsElapsed
			}
			if !hasFirstStatus {
				hasFirstStatus = true
				firstElapsed = st.SecondsElapsed
			}
			if !hasUpload && st.FilesDone > 0 {
				hasUpload = true
				uploadElapsed = st.SecondsElapsed
			}
			if !warnSeen && st.ErrorCount > 0 {
				warnSeen = true
				warnCount = st.ErrorCount
			}
		case "summary":
			hasSummary = true
			totalFiles = st.TotalFilesProcessed
			totalBytes = st.TotalBytesProcessed
			totalDur = st.TotalDuration
			snapID = st.SnapshotID
			if st.BackupStart != "" {
				if t, err := time.Parse(time.RFC3339Nano, st.BackupStart); err == nil {
					backupStart = t
				}
			}
			if st.BackupEnd != "" {
				if t, err := time.Parse(time.RFC3339Nano, st.BackupEnd); err == nil {
					backupEnd = t
				}
			}
		case "exit_error":
			exitMsg = st.Message
			if exitMsg == "" {
				exitMsg = "restic 退出码 " + strconv.FormatInt(st.Code, 10)
			}
		case "error":
			if st.Message != "" && exitMsg == "" {
				exitMsg = st.Message
			}
		}
	}

	// 状态判定
	if running {
		s.Status = "running"
	} else if exitMsg != "" || !hasSummary {
		s.Status = "failed"
	}

	// 时间格式化
	clock := func(t time.Time) string {
		if t.IsZero() {
			return "--:--"
		}
		return t.Format("15:04")
	}
	dateTitle := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("01-02 15:04")
	}
	// 基准时间：优先用 summary.backup_start，其次 backup_end，兜底日志文件名时间戳
	base := backupStart
	if base.IsZero() {
		base = backupEnd
	}
	if base.IsZero() {
		base = fileTime
	}
	if base.IsZero() {
		base = time.Now()
	}
	firstStatusAt := base.Add(time.Duration(firstElapsed) * time.Second)
	uploadStartAt := base.Add(time.Duration(uploadElapsed) * time.Second)

	switch s.Status {
	case "running":
		s.Title = "进行中的备份"
		s.Percent = lastPct * 100
		if totalBytes > 0 {
			s.OneLine = fmt.Sprintf("%s / %s", humanBytes(lastBytes), humanBytes(totalBytes))
		} else if totalFiles > 0 {
			s.OneLine = fmt.Sprintf("已处理 %s 个文件", formatInt(totalFiles))
		}
		if lastSpeed > 0 {
			s.Speed = humanBytes(int64(lastSpeed)) + "/s"
		}
		// 节点：开始扫描 → 开始上传
		if hasFirstStatus {
			s.Nodes = append(s.Nodes, logNode{Time: clock(firstStatusAt), Type: "start", Msg: "开始扫描源目录"})
		}
		if hasUpload {
			msg := "开始上传"
			if totalFiles > 0 {
				msg = "开始上传 " + formatInt(totalFiles) + " 个文件"
			}
			s.Nodes = append(s.Nodes, logNode{Time: clock(uploadStartAt), Type: "upload", Msg: msg})
		}
	case "failed":
		s.Title = dateTitle(base) + " 的备份"
		s.OneLine = friendlyErr(exitMsg)
		if hasFirstStatus {
			s.Nodes = append(s.Nodes, logNode{Time: clock(firstStatusAt), Type: "start", Msg: "开始扫描源目录"})
		}
		if hasUpload {
			s.Nodes = append(s.Nodes, logNode{Time: clock(uploadStartAt), Type: "upload", Msg: "开始上传"})
		}
		s.Nodes = append(s.Nodes, logNode{Time: clock(base), Type: "fail", Msg: friendlyErr(exitMsg)})
		s.ErrDetail = exitMsg
	default: // success
		s.Title = dateTitle(backupStart) + " 的备份"
		if backupStart.IsZero() {
			s.Title = dateTitle(backupEnd) + " 的备份"
		}
		// 一行结果
		parts := []string{}
		if totalFiles > 0 {
			parts = append(parts, formatInt(totalFiles)+" 个文件")
		}
		if totalBytes > 0 {
			parts = append(parts, humanBytes(totalBytes))
		}
		if totalDur > 0 {
			parts = append(parts, "用时 "+humanDur(totalDur))
		}
		s.OneLine = strings.Join(parts, " · ")
		// 节点（按时间顺序：开始 → 上传 → 警告 → 完成）
		if hasFirstStatus {
			s.Nodes = append(s.Nodes, logNode{Time: clock(firstStatusAt), Type: "start", Msg: "开始扫描源目录"})
		} else if !backupStart.IsZero() {
			s.Nodes = append(s.Nodes, logNode{Time: clock(backupStart), Type: "start", Msg: "开始扫描源目录"})
		}
		if hasUpload {
			msg := "开始上传"
			if totalFiles > 0 {
				msg = "开始上传 " + formatInt(totalFiles) + " 个文件"
			}
			s.Nodes = append(s.Nodes, logNode{Time: clock(uploadStartAt), Type: "upload", Msg: msg})
		}
		if warnSeen {
			s.Nodes = append(s.Nodes, logNode{Time: clock(backupStart), Type: "warn", Msg: fmt.Sprintf("%s 个文件无法读取，已跳过", formatInt(warnCount))})
		}
		if !backupEnd.IsZero() {
			msg := "备份完成，快照已写入云端"
			if snapID != "" {
				msg += "（" + shortID(snapID) + "）"
			}
			s.Nodes = append(s.Nodes, logNode{Time: clock(backupEnd), Type: "done", Msg: msg})
		}
	}

	// 失败且无任何节点时，兜底一个节点
	if s.Status == "failed" && len(s.Nodes) == 0 {
		s.Nodes = append(s.Nodes, logNode{Time: "--:--", Type: "fail", Msg: friendlyErr(exitMsg)})
	}
	return s
}

// friendlyErr：把 restic 原始错误翻译成人话
func friendlyErr(msg string) string {
	switch {
	case msg == "":
		return "备份失败"
	case strings.Contains(msg, "wrong password") || strings.Contains(msg, "no key found"):
		return "仓库密码错误，无法连接云端"
	case strings.Contains(msg, "already locked") || strings.Contains(msg, "unable to create lock"):
		return "仓库被锁定，请稍后重试"
	case strings.Contains(msg, "connection") || strings.Contains(msg, "timeout"):
		return "连接云端超时，请检查网络"
	default:
		// 去掉 "Fatal: " 前缀，截断
		m := strings.TrimSpace(strings.TrimPrefix(msg, "Fatal:"))
		if len(m) > 120 {
			m = m[:120] + "…"
		}
		return m
	}
}

func formatInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	// 每三位加逗号
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func humanDur(sec float64) string {
	n := int(sec)
	if n < 60 {
		return fmt.Sprintf("%d 秒", n)
	}
	m := n / 60
	if m < 60 {
		return fmt.Sprintf("%d 分 %d 秒", m, n%60)
	}
	h := m / 60
	return fmt.Sprintf("%d 时 %d 分", h, m%60)
}

// taskLogSummaryHandler：/api/tasks/{id}/log/summary?file=xxx —— 解析日志返回精简结果
func taskLogSummaryHandler(w http.ResponseWriter, r *http.Request) {
	p := afterMarker(r.URL.Path, "/api/tasks/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 3 || parts[2] != "summary" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	taskID := parts[0]
	file := filepath.Base(r.URL.Query().Get("file"))
	fp := filepath.Join(logDir(), taskID, file)
	if file == "." || file == "/" {
		writeJSON(w, 400, map[string]string{"error": "bad file"})
		return
	}
	stateMu.Lock()
	cur := runLog
	curID := running
	stateMu.Unlock()
	// 未指定文件：运行中读当前日志，否则取最新一份
	if file == "" || file == "." {
		if curID == taskID && cur != "" {
			fp = cur
		} else {
			entries, _ := os.ReadDir(filepath.Join(logDir(), taskID))
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
			if len(entries) == 0 {
				writeJSON(w, 200, logSummary{Status: "failed", OneLine: "暂无日志"})
				return
			}
			fp = filepath.Join(logDir(), taskID, entries[0].Name())
			file = entries[0].Name()
		}
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "日志不存在"})
		return
	}
	runningNow := curID == taskID && cur != "" && cur == fp
	// 从文件名解析时间戳（格式 2026-10-26_170029.log）
	var ft time.Time
	base := strings.TrimSuffix(filepath.Base(fp), ".log")
	if t, err := time.ParseInLocation("2006-01-02_150405", base, time.Local); err == nil {
		ft = t
	}
	writeJSON(w, 200, parseLogSummary(data, runningNow, ft))
}

// browseHandler：浏览 NAS 目录（路径选择器用）
// 用户视角：默认根为 /vol1/1000（当前用户主目录），隐藏系统目录与其他用户目录
func browseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Path    string `json:"path"`
		ShowAll bool   `json:"show_all"` // 显示系统/其他用户目录
	}
	_ = readBody(r, &req)
	sc := newScope(r)
	root := sc.Display[0]
	if req.Path == "" {
		req.Path = root
	}
	// 安全校验：路径必须落在当前用户可见的根目录内（防路径穿越 + 按账号隔离）
	safePath, serr := resolveSafePath(r, req.Path)
	if serr != nil {
		writeJSON(w, 400, map[string]any{
			"path": root, "dirs": []any{}, "user_root": root,
			"roots": sc.Display, "home": sc.Home,
			"error": serr.Error(),
		})
		return
	}
	req.Path = safePath
	entries, err := os.ReadDir(req.Path)
	if err != nil {
		writeJSON(w, 200, map[string]any{"path": req.Path, "dirs": []any{}, "user_root": root, "roots": sc.Display, "home": sc.Home, "error": err.Error()})
		return
	}
	type dirInfo struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
		Mtime int64  `json:"mtime"`
	}
	var dirs []dirInfo
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		hidden := strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@")
		// 其他用户目录（/vol1 下的纯数字目录且非当前用户）也视为系统范围
		self := "1000"
		if sc.ID.UID > 0 {
			self = strconv.Itoa(sc.ID.UID)
		}
		if req.Path == "/vol1" && name != self {
			if _, allDigit := strconv.Atoi(name); allDigit == nil {
				hidden = true
			}
		}
		if hidden && !req.ShowAll {
			continue
		}
		info := dirInfo{Name: name, Count: -1}
		if fi, err := e.Info(); err == nil {
			info.Mtime = fi.ModTime().Unix()
		}
		if sub, err := os.ReadDir(filepath.Join(req.Path, name)); err == nil {
			info.Count = len(sub)
		}
		dirs = append(dirs, info)
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	writeJSON(w, 200, map[string]any{"path": req.Path, "dirs": dirs, "user_root": root, "roots": sc.Display, "home": sc.Home})
}

// browseCloudHandler：浏览云盘目录（仓库「云端目录」选择用），rclone lsf 单层目录
func browseCloudHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Cloud  string `json:"cloud"`  // 旧参数：天翼云盘 / 115（走 ol 中转）
		Remote string `json:"remote"` // 完整远端名："ol:天翼云盘" 或 "wd-xxxxxxxx"（WebDAV 直连）
		Path   string `json:"path"`   // 云盘内路径，空 = 根
		// 临时 WebDAV 参数（新建仓库时 remote 尚未生成，直接用地址+账号浏览）
		DavURL    string `json:"dav_url"`
		DavUser   string `json:"dav_user"`
		DavPass   string `json:"dav_pass"`
		DavVendor string `json:"dav_vendor"`
	}
	_ = readBody(r, &req)
	if req.Remote == "" && req.Cloud == "" && req.DavURL == "" {
		writeJSON(w, 400, map[string]string{"error": "missing cloud"})
		return
	}

	// 临时 WebDAV（新建仓库预览）：用 :webdav: 动态参数列目录，不落 rclone.conf
	// 注意：路径必须紧跟 :webdav:，flag 放最后（rclone 语法）
	var tmpArgs []string
	if req.DavURL != "" {
		vendor := req.DavVendor
		if vendor == "" {
			vendor = "other"
		}
		webdavPath := ":webdav:"
		if p := strings.Trim(req.Path, "/"); p != "" {
			webdavPath += p
		}
		tmpArgs = []string{"lsf", webdavPath, "--webdav-url", strings.TrimSpace(req.DavURL), "--webdav-vendor", vendor, "--dirs-only"}
		if u := strings.TrimSpace(req.DavUser); u != "" {
			tmpArgs = append(tmpArgs, "--webdav-user", u)
		}
		if p := strings.TrimSpace(req.DavPass); p != "" {
			if ob := rcloneObscure(p); ob != "" {
				tmpArgs = append(tmpArgs, "--webdav-pass", ob)
			}
		}
	}

	remote := req.Remote
	if remote == "" {
		remote = "ol:" + req.Cloud
	}
	if !strings.Contains(remote, ":") {
		remote += ":" // 纯远端名（WebDAV）必须带冒号，否则 rclone 当本地路径
	}
	if req.Path != "" {
		remote += "/" + strings.Trim(req.Path, "/")
	}

	var cmd *exec.Cmd
	if tmpArgs != nil {
		cmd = exec.Command(binPath("rclone"), tmpArgs...)
	} else {
		cmd = exec.Command(binPath("rclone"), "lsf", remote, "--dirs-only")
	}
	cmd.Env = rcloneEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeJSON(w, 200, map[string]any{"path": req.Path, "dirs": []string{}, "error": strings.TrimSpace(string(out))})
		return
	}
	var dirs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			dirs = append(dirs, strings.TrimSuffix(line, "/"))
		}
	}
	sort.Strings(dirs)
	writeJSON(w, 200, map[string]any{"path": req.Path, "dirs": dirs})
}

// mkdirCloudHandler：在云盘上创建空白目录（rclone mkdir）
func mkdirCloudHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Cloud  string `json:"cloud"`
		Remote string `json:"remote"` // 完整远端名（WebDAV：wd-xxxxxxxx）
		Path   string `json:"path"`   // 完整云端路径
		// 临时 WebDAV 参数（新建仓库时 remote 未生成）
		DavURL    string `json:"dav_url"`
		DavUser   string `json:"dav_user"`
		DavPass   string `json:"dav_pass"`
		DavVendor string `json:"dav_vendor"`
	}
	_ = readBody(r, &req)

	// 临时 WebDAV：用 :webdav: 动态参数（路径紧跟 :webdav:，flag 放最后）
	var tmpArgs []string
	if req.DavURL != "" {
		vendor := req.DavVendor
		if vendor == "" {
			vendor = "other"
		}
		webdavPath := ":webdav:" + strings.Trim(req.Path, "/")
		tmpArgs = []string{"mkdir", webdavPath, "--webdav-url", strings.TrimSpace(req.DavURL), "--webdav-vendor", vendor}
		if u := strings.TrimSpace(req.DavUser); u != "" {
			tmpArgs = append(tmpArgs, "--webdav-user", u)
		}
		if p := strings.TrimSpace(req.DavPass); p != "" {
			if ob := rcloneObscure(p); ob != "" {
				tmpArgs = append(tmpArgs, "--webdav-pass", ob)
			}
		}
	}

	remote := req.Remote
	if remote == "" {
		remote = "ol:" + req.Cloud
	}
	if remote == "ol:" && strings.Trim(req.Path, "/") == "" && tmpArgs == nil {
		writeJSON(w, 400, map[string]string{"error": "missing cloud or path"})
		return
	}
	if !strings.Contains(remote, ":") {
		remote += ":" // 纯远端名（WebDAV）必须带冒号，否则 rclone 当本地路径
	}
	remote = remote + "/" + strings.Trim(req.Path, "/")

	var cmd *exec.Cmd
	if tmpArgs != nil {
		cmd = exec.Command(binPath("rclone"), tmpArgs...)
	} else {
		cmd = exec.Command(binPath("rclone"), "mkdir", remote)
	}
	cmd.Env = rcloneEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": strings.TrimSpace(string(out))})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// davErrHint：把 rclone 的报错翻译成用户能懂的原因
func davErrHint(out string) string {
	s := strings.ToLower(out)
	switch {
	case strings.Contains(s, "401") || strings.Contains(s, "unauthorized"):
		return "账号或密码错误（服务器返回 401）"
	case strings.Contains(s, "403") || strings.Contains(s, "forbidden"):
		return "服务器拒绝访问（403）：账号权限不足，或该路径不允许访问"
	case strings.Contains(s, "405") || strings.Contains(s, "method not allowed"):
		return "该地址不是 WebDAV 服务（405）：WebDAV 通常有专门的路径后缀，例如 OpenList 要加 /dav、群晖要加 /home 或路径含 /dav"
	case strings.Contains(s, "404") || strings.Contains(s, "object not found") || strings.Contains(s, "directory not found"):
		return "地址路径不存在（404）：检查 URL 的路径部分（WebDAV 通常以 /dav 结尾）"
	case strings.Contains(s, "x509") || strings.Contains(s, "certificate"):
		return "HTTPS 证书无效：自签名证书需先在系统里信任，或改用 http://"
	case strings.Contains(s, "connection refused"):
		return "连接被拒绝：地址或端口不对，或服务未启动"
	case strings.Contains(s, "no such host") || strings.Contains(s, "lookup"):
		return "域名无法解析：检查地址拼写和网络 DNS"
	case strings.Contains(s, "i/o timeout") || strings.Contains(s, "timed out") || strings.Contains(s, "deadline exceeded"):
		return "连接超时：地址/端口可能不对，或被防火墙拦截"
	case strings.Contains(s, "unsupported protocol scheme") || strings.Contains(s, "invalid scheme"):
		return "地址格式不对：需要以 http:// 或 https:// 开头"
	case strings.Contains(s, "too many redirects"):
		return "重定向过多：地址可能不对（检查路径是否多写或少写了）"
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	if len(line) > 160 {
		line = line[:160] + "…"
	}
	return "连接失败：" + line
}

// testDavHandler：WebDAV 连接测试（添加/编辑仓库时诊断问题）
// POST {remote?, dav_url, dav_user, dav_pass} —— remote 非空且密码留空时测已保存的远端
func testDavHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Remote    string `json:"remote"`
		DavURL    string `json:"dav_url"`
		DavUser   string `json:"dav_user"`
		DavPass   string `json:"dav_pass"`
		DavVendor string `json:"dav_vendor"`
	}
	_ = readBody(r, &req)
	if req.DavVendor == "" {
		req.DavVendor = "other"
	}

	// 已保存仓库（编辑时密码留空）：直接用 rclone.conf 里已存的远端测
	if req.Remote != "" && req.DavPass == "" && req.DavURL == "" {
		remote := req.Remote
		if !strings.Contains(remote, ":") {
			remote += ":"
		}
		cmd := exec.Command(binPath("rclone"), "lsf", remote, "--max-depth", "1", "--dirs-only",
			"--contimeout", "10s", "--timeout", "20s")
		cmd.Env = rcloneEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "error": davErrHint(string(out))})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "dirs": strings.Count(strings.TrimSpace(string(out)), "\n") + 1})
		return
	}

	url := strings.TrimSpace(req.DavURL)
	if url == "" {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "请先填写服务器地址"})
		return
	}
	if !strings.Contains(url, "://") {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "地址需要以 http:// 或 https:// 开头，例如：https://dav.example.com:5006/dav"})
		return
	}

	args := []string{"lsf", ":webdav:", "--webdav-url", url, "--webdav-vendor", req.DavVendor,
		"--max-depth", "1", "--dirs-only", "--contimeout", "10s", "--timeout", "20s"}
	if u := strings.TrimSpace(req.DavUser); u != "" {
		args = append(args, "--webdav-user", u)
	}
	if p := strings.TrimSpace(req.DavPass); p != "" {
		if ob := rcloneObscure(p); ob != "" {
			args = append(args, "--webdav-pass", ob)
		}
	}
	cmd := exec.Command(binPath("rclone"), args...)
	cmd.Env = rcloneEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": davErrHint(string(out))})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "dirs": strings.Count(strings.TrimSpace(string(out)), "\n") + 1})
}

// ---------------------------------------------------------------------------
// 还原：快照列表 / 文件列表（内存缓存）/ 变化对比 / 还原执行
// ---------------------------------------------------------------------------

type resticSnapSummary struct {
	TotalFiles int64 `json:"total_files"`
	TotalBytes int64 `json:"total_bytes"`
}

// UnmarshalJSON：restic 输出的字段是 total_files_processed / total_bytes_processed，
// 对外仍用 total_files / total_bytes（前端沿用），旧缓存里的同名字段也兼容。
func (s *resticSnapSummary) UnmarshalJSON(b []byte) error {
	var raw struct {
		FilesProc  int64 `json:"total_files_processed"`
		BytesProc  int64 `json:"total_bytes_processed"`
		FilesPlain int64 `json:"total_files"`
		BytesPlain int64 `json:"total_bytes"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	s.TotalFiles = raw.FilesProc
	if s.TotalFiles == 0 {
		s.TotalFiles = raw.FilesPlain
	}
	s.TotalBytes = raw.BytesProc
	if s.TotalBytes == 0 {
		s.TotalBytes = raw.BytesPlain
	}
	return nil
}

type resticSnap struct {
	ID       string             `json:"id"`
	ShortID  string             `json:"short_id"`
	Time     string             `json:"time"`
	Hostname string             `json:"hostname"`
	Tags     []string           `json:"tags"`
	Paths    []string           `json:"paths"`
	Summary  *resticSnapSummary `json:"summary"`
}

// runResticCapture：执行 restic 并捕获 stdout（stderr 落日志文件）。
// 云端偶发锁文件悬空会让 restic 无限重试，必须加超时避免 UI 挂死。
func runResticCapture(repo *Repo, args []string, logPath string) ([]byte, error) {
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath("restic"), args...)
	cmd.Env = append(os.Environ(), resticEnv(repo)...)
	cmd.Stdout = &out
	if logPath != "" {
		if lf, err := os.Create(logPath); err == nil {
			defer lf.Close()
			cmd.Stderr = lf
			fmt.Fprintf(lf, "$ restic %s\n", strings.Join(args, " "))
		}
	}
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("命令超时（4 分钟）——云端可能不稳定，请稍后重试")
	}
	return out.Bytes(), err
}

// ---- 应用级快照列表缓存（每仓库一份 JSON） ----
// 还原页打开时间线不再每次实时查云盘：默认读本地缓存，
// 备份成功后自动刷新，前端提供手动「刷新缓存」强制实时查。

type snapCacheEntry struct {
	RepoID      string       `json:"repo_id"`
	GeneratedAt string       `json:"generated_at"` // 本地时间
	Count       int          `json:"count"`
	LatestTime  string       `json:"latest_time"`
	Snaps       []resticSnap `json:"snaps"` // 仓库全部快照（未按任务过滤）
}

var snapCacheMu sync.Mutex

func snapCachePath(repoID string) string {
	return filepath.Join(varDir(), "cache", "snapshots-"+repoID+".json")
}

// refreshSnapCache：实时执行 restic snapshots 并写入本地缓存，失败返回 nil
func refreshSnapCache(repo *Repo) *snapCacheEntry {
	snapCacheMu.Lock()
	defer snapCacheMu.Unlock()
	_ = os.MkdirAll(filepath.Join(varDir(), "cache"), 0o755)
	lp := filepath.Join(logDir(), "snapshots-repo-"+repo.ID+".log")
	out, err := runResticCapture(repo, []string{"snapshots", "--json"}, lp)
	if err != nil {
		return nil
	}
	var snaps []resticSnap
	if json.Unmarshal(out, &snaps) != nil {
		return nil
	}
	latest := ""
	for _, s := range snaps {
		if s.Time > latest {
			latest = s.Time
		}
	}
	e := &snapCacheEntry{
		RepoID:      repo.ID,
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		Count:       len(snaps),
		LatestTime:  latest,
		Snaps:       snaps,
	}
	data, _ := json.Marshal(e)
	tmp := snapCachePath(repo.ID) + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, snapCachePath(repo.ID))
	}
	return e
}

// loadSnapCache：读缓存文件（不存在 / 损坏 / 仓库不匹配返回 nil）
func loadSnapCache(repoID string) *snapCacheEntry {
	data, err := os.ReadFile(snapCachePath(repoID))
	if err != nil {
		return nil
	}
	var e snapCacheEntry
	if json.Unmarshal(data, &e) != nil || e.RepoID != repoID || e.Snaps == nil {
		return nil
	}
	return &e
}

// ---- 快照文件列表缓存（内存 LRU 2 份 + 磁盘缓存，含目录树全量节点） ----
//
// restic ls 会把整份快照的文件清单吐出来，几十万文件时要跑一分钟左右，
// 而目录浏览只是在这份清单里按父目录过滤。所以：第一次流式读取并落盘，
// 之后（含进程重启后）直接从磁盘缓存秒开；读取过程中通过 202 状态回报进度。

type snapNode struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
}

type snapListing struct {
	Roots  []string             // 快照的源路径（绝对）
	Nodes  []snapNode           // 全部节点（目录+文件）
	byPath map[string]*snapNode `json:"-"`
	Files  []snapNode           // 仅文件（搜索用）
	total  int
}

// lsJob 记录一次「读取整份快照清单」的后台任务，供前端轮询进度。
type lsJob struct {
	key    string
	mu     sync.Mutex
	run    bool
	parsed int
	start  time.Time
	err    string
	sl     *snapListing
	done   chan struct{}
}

func (j *lsJob) snapshot() (run bool, parsed int, elapsed int, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.run, j.parsed, int(time.Since(j.start).Seconds()), j.err
}

func (j *lsJob) setParsed(n int) {
	j.mu.Lock()
	j.parsed = n
	j.mu.Unlock()
}

var (
	lsJobsMu sync.Mutex
	lsJobs   = map[string]*lsJob{}
)

var (
	lsMu    sync.Mutex
	lsCache = map[string]*snapListing{} // key: repoID|snapshotID
	lsOrder []string
)

func lsCacheGet(key string) *snapListing {
	lsMu.Lock()
	defer lsMu.Unlock()
	return lsCache[key]
}

func lsCachePut(key string, sl *snapListing) {
	lsMu.Lock()
	defer lsMu.Unlock()
	if _, ok := lsCache[key]; ok {
		return
	}
	lsCache[key] = sl
	lsOrder = append(lsOrder, key)
	for len(lsOrder) > 2 {
		delete(lsCache, lsOrder[0])
		lsOrder = lsOrder[1:]
	}
}

// ---- 磁盘缓存：cache/ls-<repo>-<snap>.json.gz ----

func lsDiskPath(repoID, snapID string) string {
	return filepath.Join(varDir(), "cache", "ls-"+repoID+"-"+snapID+".json.gz")
}

type lsDiskFile struct {
	SnapID  string     `json:"snapshot_id"`
	BuiltAt string     `json:"built_at"`
	Roots   []string   `json:"roots"`
	Total   int        `json:"total_files"`
	Nodes   []snapNode `json:"nodes"`
}

func loadLsDisk(repoID, snapID string) *snapListing {
	f, err := os.Open(lsDiskPath(repoID, snapID))
	if err != nil {
		return nil
	}
	defer f.Close()
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return nil
	}
	defer zr.Close()
	var d lsDiskFile
	if json.NewDecoder(zr).Decode(&d) != nil || d.SnapID != snapID || len(d.Nodes) == 0 {
		return nil
	}
	sl := &snapListing{Roots: d.Roots, Nodes: d.Nodes, total: d.Total}
	sl.reindex()
	return sl
}

func saveLsDisk(repoID, snapID string, sl *snapListing) {
	dir := filepath.Join(varDir(), "cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	p := lsDiskPath(repoID, snapID)
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	zw := gzip.NewWriter(bw)
	d := lsDiskFile{SnapID: snapID, BuiltAt: time.Now().Format("2006-01-02 15:04:05"),
		Roots: sl.Roots, Total: sl.total, Nodes: sl.Nodes}
	err = json.NewEncoder(zw).Encode(&d)
	if err == nil {
		err = zw.Close()
	} else {
		_ = zw.Close()
	}
	if err == nil {
		err = bw.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = os.Rename(tmp, p)
	pruneLsDisk(4)
}

// pruneLsDisk 只保留最近使用的若干份清单，避免几十万文件的快照把磁盘吃满。
func pruneLsDisk(keep int) {
	ents, err := os.ReadDir(filepath.Join(varDir(), "cache"))
	if err != nil {
		return
	}
	type fi struct {
		p string
		t time.Time
	}
	var all []fi
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(n, "ls-") || !strings.HasSuffix(n, ".json.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		all = append(all, fi{filepath.Join(varDir(), "cache", n), info.ModTime()})
	}
	if len(all) <= keep {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.After(all[j].t) })
	for _, f := range all[keep:] {
		_ = os.Remove(f.p)
	}
}

func (sl *snapListing) reindex() {
	sl.byPath = make(map[string]*snapNode, len(sl.Nodes))
	sl.Files = sl.Files[:0]
	for i := range sl.Nodes {
		n := &sl.Nodes[i]
		sl.byPath[n.Path] = n
		if n.Type != "dir" {
			sl.Files = append(sl.Files, *n)
		}
	}
	if sl.total == 0 {
		sl.total = len(sl.Files)
	}
}

// buildListing 流式跑 restic ls --json，边解析边回报进度，完成后落盘。
func buildListing(repo *Repo, snapID string, job *lsJob) (*snapListing, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath("restic"), "ls", "--json", snapID)
	cmd.Env = append(os.Environ(), resticEnv(repo)...)
	lp := filepath.Join(logDir(), "ls-"+snapID+".log")
	if lf, err := os.Create(lp); err == nil {
		defer lf.Close()
		cmd.Stderr = lf
		fmt.Fprintf(lf, "$ restic ls --json %s\n", snapID)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sl := &snapListing{byPath: map[string]*snapNode{}}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 256*1024), 8*1024*1024)
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var probe map[string]any
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue
		}
		if st, _ := probe["struct_type"].(string); st == "snapshot" {
			if ps, ok := probe["paths"].([]any); ok {
				for _, p := range ps {
					if s, ok := p.(string); ok {
						sl.Roots = append(sl.Roots, s)
					}
				}
			}
			continue
		}
		var node snapNode
		if json.Unmarshal([]byte(line), &node) != nil || node.Path == "" {
			continue
		}
		sl.Nodes = append(sl.Nodes, node)
		n++
		if n%5000 == 0 {
			job.setParsed(n)
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()
	job.setParsed(n)
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("读取快照清单超时（10 分钟），云端可能不稳定，请稍后重试")
	}
	if scanErr != nil {
		return nil, fmt.Errorf("读取快照文件列表失败（详见日志 %s）: %v", lp, scanErr)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("读取快照文件列表失败（详见日志 %s）: %v", lp, waitErr)
	}
	sl.reindex()
	saveLsDisk(repo.ID, snapID, sl)
	return sl, nil
}

func startListing(repo *Repo, snapID string) *lsJob {
	key := repo.ID + "|" + snapID
	lsJobsMu.Lock()
	j := lsJobs[key]
	if j == nil {
		j = &lsJob{key: key, done: make(chan struct{})}
		lsJobs[key] = j
	}
	lsJobsMu.Unlock()

	j.mu.Lock()
	if j.run || j.sl != nil {
		j.mu.Unlock()
		return j
	}
	j.run = true
	j.start = time.Now()
	j.err = ""
	j.mu.Unlock()

	go func() {
		sl, err := buildListing(repo, snapID, j)
		j.mu.Lock()
		j.run = false
		if err != nil {
			j.err = err.Error()
		} else {
			j.sl = sl
		}
		close(j.done)
		j.mu.Unlock()
		if sl != nil {
			lsCachePut(key, sl)
		} else {
			// 失败的任务从注册表移除，下次请求可以重试
			lsJobsMu.Lock()
			if lsJobs[key] == j {
				delete(lsJobs, key)
			}
			lsJobsMu.Unlock()
		}
	}()
	return j
}

// getSnapshotListing 阻塞式获取（差异对比用）：内存 → 磁盘 → 现跑。
func getSnapshotListing(repo *Repo, snapID string) (*snapListing, error) {
	key := repo.ID + "|" + snapID
	if sl := lsCacheGet(key); sl != nil {
		return sl, nil
	}
	if sl := loadLsDisk(repo.ID, snapID); sl != nil {
		lsCachePut(key, sl)
		return sl, nil
	}
	j := startListing(repo, snapID)
	<-j.done
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.err != "" {
		return nil, errors.New(j.err)
	}
	return j.sl, nil
}

// ensureListing 非阻塞式获取（目录浏览用）：拿不到就返回 job 让前端轮询进度。
func ensureListing(repo *Repo, snapID string) (*snapListing, *lsJob) {
	key := repo.ID + "|" + snapID
	if sl := lsCacheGet(key); sl != nil {
		return sl, nil
	}
	if sl := loadLsDisk(repo.ID, snapID); sl != nil {
		lsCachePut(key, sl)
		return sl, nil
	}
	return nil, startListing(repo, snapID)
}

// ---- 变化对比（与上一份快照比），结果缓存供文件列表加徽章 ----

var (
	diffMu    sync.Mutex
	diffCache = map[string]map[string]string{} // key: repoID|snapID → path→add|mod
)

func computeDiff(repo *Repo, prevID, curID string) (map[string]int, error) {
	prev, err := getSnapshotListing(repo, prevID)
	if err != nil {
		return nil, err
	}
	cur, err := getSnapshotListing(repo, curID)
	if err != nil {
		return nil, err
	}
	prevFiles := map[string]*snapNode{}
	for i := range prev.Nodes {
		if prev.Nodes[i].Type != "dir" {
			prevFiles[prev.Nodes[i].Path] = &prev.Nodes[i]
		}
	}
	badges := map[string]string{}
	counts := map[string]int{"added": 0, "changed": 0, "removed": 0}
	for i := range cur.Nodes {
		n := &cur.Nodes[i]
		if n.Type == "dir" {
			continue
		}
		if p, ok := prevFiles[n.Path]; !ok {
			badges[n.Path] = "add"
			counts["added"]++
		} else if p.Size != n.Size || p.Mtime != n.Mtime {
			badges[n.Path] = "mod"
			counts["changed"]++
		}
	}
	for p := range prevFiles {
		if _, ok := cur.byPath[p]; !ok {
			counts["removed"]++
		}
	}
	diffMu.Lock()
	diffCache[repo.ID+"|"+curID] = badges
	diffMu.Unlock()
	return counts, nil
}

// ---- API：快照列表 ----

// taskSnapshotsHandler：默认读本地缓存，refresh=1 强制实时查并回写缓存
func taskSnapshotsHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/tasks/"), "/snapshots")
	refresh := r.URL.Query().Get("refresh") == "1"
	cfgMu.Lock()
	t := findTask(id)
	var cp *Task
	if t != nil {
		c := *t
		cp = &c
	}
	cfgMu.Unlock()
	if cp == nil {
		writeJSON(w, 404, map[string]string{"error": "任务不存在"})
		return
	}
	cfgMu.Lock()
	var rp Repo
	if repo := findRepo(cp.RepoID); repo != nil {
		rp = *repo
	}
	cfgMu.Unlock()
	if rp.ID == "" {
		writeJSON(w, 404, map[string]string{"error": "仓库不存在"})
		return
	}
	// 三级取数：强制刷新 → 本地缓存 → 实时查并回写缓存
	var all []resticSnap
	var cachedAt string
	if refresh {
		if e := refreshSnapCache(&rp); e != nil {
			all, cachedAt = e.Snaps, e.GeneratedAt
		}
	} else if e := loadSnapCache(rp.ID); e != nil {
		all, cachedAt = e.Snaps, e.GeneratedAt
	}
	if all == nil {
		e := refreshSnapCache(&rp)
		if e == nil {
			writeJSON(w, 500, map[string]string{"error": "读取快照列表失败：云端查询出错，请稍后重试"})
			return
		}
		all, cachedAt = e.Snaps, e.GeneratedAt
	}
	// 只保留与本任务源路径相关的快照，按时间倒序
	var snaps []resticSnap
	for _, s := range all {
		for _, tp := range cp.Paths {
			for _, sp := range s.Paths {
				if tp == sp || strings.HasPrefix(tp, sp+"/") || strings.HasPrefix(sp, tp+"/") {
					snaps = append(snaps, s)
					tp, sp = "", ""
					break
				}
			}
			if tp == "" {
				break
			}
		}
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time > snaps[j].Time })
	if snaps == nil {
		snaps = []resticSnap{}
	}
	writeJSON(w, 200, map[string]any{"snapshots": snaps, "task_paths": cp.Paths, "cached_at": cachedAt, "repo_snap_count": len(all)})
}

// ---- API：仓库维度（仓库 Tab） ----

// pathOverlap：任务源路径与快照路径是否有包含关系（任一方是另一方的前缀）
func pathOverlap(taskPaths, snapPaths []string) bool {
	for _, tp := range taskPaths {
		if tp == "" {
			continue
		}
		for _, sp := range snapPaths {
			if tp == sp || strings.HasPrefix(tp, sp+"/") || strings.HasPrefix(sp, tp+"/") {
				return true
			}
		}
	}
	return false
}

// repoSnapItem：仓库视角的一条快照，附带归属任务（tag 优先，paths 兜底）
type repoSnapItem struct {
	resticSnap
	TaskID   string `json:"task_id"`
	TaskName string `json:"task_name"`
}

type repoTaskStat struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Paths      []string `json:"paths"`
	SnapCount  int      `json:"snap_count"`
	LastTime   string   `json:"last_time"`
	LastStatus string   `json:"last_status"`
}

// repoSnapshotsHandler：GET /api/repos/:id/snapshots
// 返回该仓库的全量快照 + 每条快照归属的任务；refresh=1 强制实时查云端。
func repoSnapshotsHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/repos/"), "/snapshots")
	refresh := r.URL.Query().Get("refresh") == "1"

	cfgMu.Lock()
	var rp Repo
	if repo := findRepo(id); repo != nil {
		rp = *repo
	}
	var tasks []Task
	if rp.ID != "" {
		for _, t := range cfg.Tasks {
			if t.RepoID == rp.ID {
				tasks = append(tasks, t)
			}
		}
	}
	cfgMu.Unlock()
	if rp.ID == "" {
		writeJSON(w, 404, map[string]string{"error": "仓库不存在"})
		return
	}

	// 三级取数：强制刷新 → 本地缓存 → 实时查并回写缓存
	var all []resticSnap
	var cachedAt string
	if refresh {
		if e := refreshSnapCache(&rp); e != nil {
			all, cachedAt = e.Snaps, e.GeneratedAt
		}
	} else if e := loadSnapCache(rp.ID); e != nil {
		all, cachedAt = e.Snaps, e.GeneratedAt
	}
	if all == nil {
		e := refreshSnapCache(&rp)
		if e == nil {
			writeJSON(w, 500, map[string]string{"error": "读取快照列表失败：云端查询出错，请稍后重试"})
			return
		}
		all, cachedAt = e.Snaps, e.GeneratedAt
	}

	byID := map[string]*Task{}
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}
	items := make([]repoSnapItem, 0, len(all))
	for _, s := range all {
		it := repoSnapItem{resticSnap: s}
		// 1) 优先看备份时打的 tag（task:<id>）
		for _, tg := range s.Tags {
			if strings.HasPrefix(tg, "task:") {
				if t, ok := byID[strings.TrimPrefix(tg, "task:")]; ok {
					it.TaskID, it.TaskName = t.ID, t.Name
				}
			}
		}
		// 2) 历史快照没有 tag，按源路径前缀兜底
		if it.TaskID == "" {
			for i := range tasks {
				if pathOverlap(tasks[i].Paths, s.Paths) {
					it.TaskID, it.TaskName = tasks[i].ID, tasks[i].Name
					break
				}
			}
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Time > items[j].Time })

	var totalBytes int64
	for _, it := range items {
		if it.Summary != nil {
			totalBytes += it.Summary.TotalBytes
		}
	}

	stats := make([]repoTaskStat, 0, len(tasks))
	for i := range tasks {
		st := repoTaskStat{ID: tasks[i].ID, Name: tasks[i].Name, Paths: tasks[i].Paths,
			LastStatus: tasks[i].LastStatus}
		for _, it := range items {
			if it.TaskID == st.ID {
				st.SnapCount++
				if it.Time > st.LastTime {
					st.LastTime = it.Time
				}
			}
		}
		stats = append(stats, st)
	}

	writeJSON(w, 200, map[string]any{
		"repo_id": rp.ID, "repo_name": rp.Name, "cloud": rp.Cloud, "uri": rp.URI,
		"snapshots": items, "tasks": stats,
		"total": len(items), "total_bytes": totalBytes,
		"latest_time": func() string {
			if len(items) > 0 {
				return items[0].Time
			}
			return ""
		}(),
		"cached_at": cachedAt,
	})
}

// repoSummaryHandler：GET /api/repos/summary —— 只读本地缓存，绝不查云端。
// 仓库列表页用它出「快照数 / 占用 / 最近快照」，没缓存时 cached=false，前端提示手动刷新。
func repoSummaryHandler(w http.ResponseWriter, r *http.Request) {
	cfgMu.Lock()
	repos := make([]Repo, len(cfg.Repos))
	copy(repos, cfg.Repos)
	taskCount := map[string]int{}
	for _, t := range cfg.Tasks {
		taskCount[t.RepoID]++
	}
	cfgMu.Unlock()

	out := make([]map[string]any, 0, len(repos))
	for _, rp := range repos {
		row := map[string]any{
			"id": rp.ID, "name": rp.Name, "cloud": rp.Cloud, "uri": rp.URI,
			"task_count": taskCount[rp.ID], "cached": false,
			"count": 0, "total_bytes": int64(0), "latest_time": "", "cached_at": "",
		}
		if e := loadSnapCache(rp.ID); e != nil {
			var bytes int64
			for _, s := range e.Snaps {
				if s.Summary != nil {
					bytes += s.Summary.TotalBytes
				}
			}
			row["cached"] = true
			row["count"] = len(e.Snaps)
			row["total_bytes"] = bytes
			row["latest_time"] = e.LatestTime
			row["cached_at"] = e.GeneratedAt
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{"repos": out})
}

// ---- API：文件浏览 / 搜索 ----

func taskFilesHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/tasks/"), "/files")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	snapID := strings.TrimSpace(r.URL.Query().Get("snapshot"))
	dir := strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("dir")), "/")
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit < 0 {
		limit = 0
	}
	cfgMu.Lock()
	t := findTask(id)
	var cp *Task
	if t != nil {
		c := *t
		cp = &c
	}
	var rp Repo
	if t != nil {
		if repo := findRepo(t.RepoID); repo != nil {
			rp = *repo
		}
	}
	cfgMu.Unlock()
	if cp == nil || rp.ID == "" {
		writeJSON(w, 404, map[string]string{"error": "任务或仓库不存在"})
		return
	}
	if snapID == "" {
		writeJSON(w, 400, map[string]string{"error": "缺少 snapshot 参数"})
		return
	}
	sl, job := ensureListing(&rp, snapID)
	if sl == nil {
		// 首次读取大快照要跑一分钟左右，先回 200 + loading，前端轮询进度
		run, parsed, elapsed, errMsg := job.snapshot()
		resp := map[string]any{
			"dir": dir, "q": q, "items": []any{}, "loading": run,
			"parsed": parsed, "elapsed": elapsed, "snapshot": snapID,
		}
		if errMsg != "" {
			resp["error"] = errMsg
			resp["loading"] = false
		}
		writeJSON(w, 200, resp)
		return
	}
	diffMu.Lock()
	badges := diffCache[rp.ID+"|"+snapID]
	diffMu.Unlock()

	type item struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Parent string `json:"parent,omitempty"`
		IsDir  bool   `json:"is_dir"`
		Size   int64  `json:"size"`
		Badge  string `json:"badge,omitempty"`
	}
	mk := func(n *snapNode, parent string) item {
		b := ""
		if badges != nil {
			b = badges[n.Path]
		}
		return item{Name: n.Name, Path: n.Path, Parent: parent, IsDir: n.Type == "dir", Size: n.Size, Badge: b}
	}
	var items []item
	addNode := func(n *snapNode, parent string) { items = append(items, mk(n, parent)) }

	if q != "" {
		// 全快照搜索（仅文件，最多 300 条）
		lq := strings.ToLower(q)
		for i := range sl.Files {
			n := &sl.Files[i]
			if strings.Contains(strings.ToLower(n.Name), lq) {
				addNode(n, filepath.Dir(n.Path))
				if len(items) >= 300 {
					break
				}
			}
		}
	} else if dir == "" {
		// 根视图：快照的源目录
		for _, root := range sl.Roots {
			base := filepath.Base(root)
			if base == "/" || base == "." {
				base = root
			}
			n := sl.byPath[root]
			if n == nil {
				n = &snapNode{Name: base, Type: "dir", Path: root}
			}
			addNode(n, "")
		}
	} else {
		n, ok := sl.byPath[dir]
		if !ok || n.Type != "dir" {
			writeJSON(w, 400, map[string]string{"error": "目录不存在于该快照"})
			return
		}
		parent := dir
		for i := range sl.Nodes {
			nd := &sl.Nodes[i]
			if nd.Path != dir && filepath.Dir(nd.Path) == dir {
				addNode(nd, parent)
			}
		}
	}
	// 目录在前、按名排序（搜索分支保持原顺序）
	if q == "" {
		sort.Slice(items, func(i, j int) bool {
			if items[i].IsDir != items[j].IsDir {
				return items[i].IsDir
			}
			return items[i].Name < items[j].Name
		})
	}
	if items == nil {
		items = []item{}
	}
	// 目录下文件可能上万，按 limit 截断，避免一次吐太多
	dirTotal := len(items)
	truncated := false
	if limit > 0 && len(items) > limit {
		items = items[:limit]
		truncated = true
	}
	writeJSON(w, 200, map[string]any{
		"dir": dir, "q": q, "items": items, "total_files": sl.total,
		"roots": sl.Roots, "dir_total": dirTotal, "truncated": truncated,
	})
}

// ---- API：变化对比 ----

func taskSnapDiffHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/tasks/"), "/snapdiff")
	s2 := strings.TrimSpace(r.URL.Query().Get("cur"))
	s1 := strings.TrimSpace(r.URL.Query().Get("prev"))
	if s2 == "" || s1 == "" {
		writeJSON(w, 400, map[string]string{"error": "缺少 cur/prev 参数"})
		return
	}
	cfgMu.Lock()
	t := findTask(id)
	var rp Repo
	if t != nil {
		if repo := findRepo(t.RepoID); repo != nil {
			rp = *repo
		}
	}
	cfgMu.Unlock()
	if rp.ID == "" {
		writeJSON(w, 404, map[string]string{"error": "任务或仓库不存在"})
		return
	}
	counts, err := computeDiff(&rp, s1, s2)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"added": counts["added"], "changed": counts["changed"], "removed": counts["removed"]})
}

// ---- API：执行还原 ----

func taskRestoreHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/tasks/"), "/restore")
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Snapshot string   `json:"snapshot"`
		SnapTime string   `json:"snapshot_time"`
		Includes []string `json:"includes"`
		Dest     string   `json:"dest"`
		Target   string   `json:"target"`
		Conflict string   `json:"conflict"`
	}
	if err := readBody(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "参数错误"})
		return
	}
	if req.Snapshot == "" || len(req.Includes) == 0 {
		writeJSON(w, 400, map[string]string{"error": "请选择快照和要还原的文件"})
		return
	}
	// 安全校验：还原路径片段不允许出现 ..（防跳出快照根）
	if ierr := safeIncludes(req.Includes); ierr != nil {
		writeJSON(w, 400, map[string]string{"error": ierr.Error()})
		return
	}
	if req.Dest != "original" && req.Dest != "custom" {
		writeJSON(w, 400, map[string]string{"error": "dest 参数错误"})
		return
	}
	validConflict := map[string]bool{"always": true, "if-changed": true, "never": true, "rename": true}
	if !validConflict[req.Conflict] {
		writeJSON(w, 400, map[string]string{"error": "conflict 参数错误"})
		return
	}
	if req.Dest == "original" && req.Conflict == "rename" {
		writeJSON(w, 400, map[string]string{"error": "原位置还原不支持保留两者，请选择覆盖/仅覆盖有变化/跳过"})
		return
	}
	if req.Dest == "custom" {
		req.Target = strings.TrimSuffix(strings.TrimSpace(req.Target), "/")
		if !filepath.IsAbs(req.Target) {
			writeJSON(w, 400, map[string]string{"error": "目标目录必须为绝对路径"})
			return
		}
		// 安全校验：还原目标必须落在用户可见根目录内（防写入任意路径）
		safeTarget, terr := resolveSafePath(r, req.Target)
		if terr != nil {
			writeJSON(w, 400, map[string]string{"error": "目标目录不合法：" + terr.Error()})
			return
		}
		req.Target = safeTarget
		if err := os.MkdirAll(req.Target, 0o755); err != nil {
			writeJSON(w, 400, map[string]string{"error": "目标目录无法创建: " + err.Error()})
			return
		}
	}
	cfgMu.Lock()
	t := findTask(id)
	var cp *Task
	var rp *Repo
	if t != nil {
		c := *t
		cp = &c
		rp = findRepo(t.RepoID)
	}
	cfgMu.Unlock()
	if cp == nil || rp == nil {
		writeJSON(w, 404, map[string]string{"error": "任务或仓库不存在"})
		return
	}
	job := RestoreJob{
		ID: "rst-" + randomID(), TaskID: cp.ID, TaskName: cp.Name,
		Snapshot: req.Snapshot, SnapshotTime: req.SnapTime,
		Dest: req.Dest, Target: req.Target, Conflict: req.Conflict,
		Includes: req.Includes, Status: "running",
		StartedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	cfgMu.Lock()
	cfg.Restores = append([]RestoreJob{job}, cfg.Restores...)
	if len(cfg.Restores) > 30 {
		cfg.Restores = cfg.Restores[:30]
	}
	_ = saveConfig()
	cfgMu.Unlock()
	go runRestore(job)
	writeJSON(w, 200, map[string]any{"ok": true, "id": job.ID})
}

// setRestoreJob：按 ID 更新还原任务并落盘
func setRestoreJob(id string, fn func(j *RestoreJob)) {
	cfgMu.Lock()
	for i := range cfg.Restores {
		if cfg.Restores[i].ID == id {
			fn(&cfg.Restores[i])
			break
		}
	}
	_ = saveConfig()
	cfgMu.Unlock()
}

var (
	rstMu    sync.Mutex
	rstStops = map[string]chan struct{}{}
)

func runRestore(j RestoreJob) {
	// 仓库（含密码）副本
	cfgMu.Lock()
	var rp Repo
	for i := range cfg.Tasks {
		if cfg.Tasks[i].ID == j.TaskID {
			if repo := findRepo(cfg.Tasks[i].RepoID); repo != nil {
				rp = *repo
			}
			break
		}
	}
	cfgMu.Unlock()
	finish := func(status, msg string, stats ...int) {
		setRestoreJob(j.ID, func(x *RestoreJob) {
			x.Status = status
			x.Message = msg
			x.FinishedAt = time.Now().Format("2006-01-02 15:04:05")
			if status == "success" {
				x.Pct = 100
			}
			if len(stats) >= 4 {
				x.Restored, x.Replaced, x.Renamed, x.Skipped = stats[0], stats[1], stats[2], stats[3]
			}
		})
	}

	stop := make(chan struct{})
	rstMu.Lock()
	rstStops[j.ID] = stop
	rstMu.Unlock()
	defer func() {
		rstMu.Lock()
		delete(rstStops, j.ID)
		rstMu.Unlock()
	}()

	_ = os.MkdirAll(logDir(), 0o755)
	logPath := filepath.Join(logDir(), j.ID+".log")
	lf, _ := os.Create(logPath)
	if lf != nil {
		defer lf.Close()
		fmt.Fprintf(lf, "[restore] task=%s snapshot=%s dest=%s target=%q conflict=%s includes=%v\n",
			j.TaskID, j.Snapshot, j.Dest, j.Target, j.Conflict, j.Includes)
	}
	if rp.ID == "" {
		finish("failed", "仓库不存在")
		return
	}

	// 静默 unlock（快照只读，但防陈旧锁）
	if ul, err := runResticCapture(&rp, []string{"unlock"}, logPath+".unlock"); err != nil {
		_ = ul
	}

	// 原位置：target=/ ；自定义：先还原到目标目录下的临时子目录，再按冲突策略搬移
	target := "/"
	var tmp string
	if j.Dest == "custom" {
		tmp = filepath.Join(j.Target, ".encbackup-rst-"+j.ID)
		target = tmp // restic 先还原进临时目录（全新无冲突），rename 等冲突策略由随后的搬移阶段落地
		_ = os.RemoveAll(tmp)
		if err := os.MkdirAll(tmp, 0o755); err != nil {
			finish("failed", "无法创建临时目录: "+err.Error())
			return
		}
	}
	// restic --overwrite 只认 always|if-changed|if-newer|never；
	// 自定义目的地还原进全新临时目录，无冲突可言，固定 always；原位置映射用户选择
	overwrite := "always"
	if j.Dest == "original" {
		switch j.Conflict {
		case "always":
			overwrite = "always"
		case "never":
			overwrite = "never"
		default:
			overwrite = "if-changed"
		}
	}
	args := []string{"restore", j.Snapshot, "--json", "--target", target, "--overwrite", overwrite}
	for _, inc := range j.Includes {
		args = append(args, "--include", inc)
	}

	cmd := exec.Command(binPath("restic"), args...)
	cmd.Env = append(os.Environ(), resticEnv(&rp)...)
	stdout, _ := cmd.StdoutPipe()
	if lf != nil {
		cmd.Stderr = lf
		fmt.Fprintf(lf, "$ restic %s\n", strings.Join(args, " "))
	}
	_ = cmd.Start()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	go func() {
		for sc.Scan() {
			line := sc.Text()
			if lf != nil {
				fmt.Fprintln(lf, line)
			}
			var st struct {
				MessageType string  `json:"message_type"`
				PercentDone float64 `json:"percent_done"`
				TotalFiles  int64   `json:"total_files"`
				TotalBytes  int64   `json:"total_bytes"`
			}
			if json.Unmarshal([]byte(line), &st) != nil {
				continue
			}
			switch st.MessageType {
			case "status":
				pct := st.PercentDone * 100
				if pct > 99 {
					pct = 99
				}
				// 内存更新即可，落盘在结束时统一做
				cfgMu.Lock()
				for i := range cfg.Restores {
					if cfg.Restores[i].ID == j.ID {
						if pct > cfg.Restores[i].Pct {
							cfg.Restores[i].Pct = pct
						}
						if st.TotalFiles > cfg.Restores[i].Files {
							cfg.Restores[i].Files = st.TotalFiles
						}
						if st.TotalBytes > cfg.Restores[i].Bytes {
							cfg.Restores[i].Bytes = st.TotalBytes
						}
					}
				}
				cfgMu.Unlock()
			case "summary":
				cfgMu.Lock()
				for i := range cfg.Restores {
					if cfg.Restores[i].ID == j.ID {
						if st.TotalFiles > 0 {
							cfg.Restores[i].Files = st.TotalFiles
						}
						if st.TotalBytes > 0 {
							cfg.Restores[i].Bytes = st.TotalBytes
						}
					}
				}
				cfgMu.Unlock()
			}
		}
	}()

	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()

	var err error
	stopped := false
	select {
	case err = <-finished:
	case <-stop:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-finished
		stopped = true
	}
	if stopped {
		if tmp != "" {
			_ = os.RemoveAll(tmp)
		}
		finish("stopped", "已手动停止")
		return
	}
	if err != nil {
		if tmp != "" {
			_ = os.RemoveAll(tmp)
		}
		finish("failed", "还原失败: "+err.Error())
		return
	}

	// 自定义目的地：从临时目录搬移到目标目录（冲突策略在这里落地）
	if j.Dest == "custom" {
		restored, replaced, skipped, renamed := 0, 0, 0, 0
		for _, inc := range j.Includes {
			src := filepath.Join(tmp, strings.TrimPrefix(filepath.Clean(inc), "/"))
			if _, err := os.Lstat(src); err != nil {
				fmt.Fprintf(lf, "[warn] 快照内未找到 %s\n", inc)
				skipped++
				continue
			}
			dst := filepath.Join(j.Target, filepath.Base(inc))
			if fi, err := os.Lstat(dst); err == nil {
				switch j.Conflict {
				case "never":
					skipped++
					continue
				case "rename":
					dst = dst + " (还原 " + time.Now().Format("2006-01-02-150405") + ")"
					renamed++
					_ = fi
				case "if-changed":
					sfi, _ := os.Lstat(src)
					if sfi != nil && sfi.Size() == fi.Size() && sfi.ModTime().Equal(fi.ModTime()) {
						skipped++
						continue
					}
					_ = os.RemoveAll(dst)
					replaced++
				default: // always
					_ = os.RemoveAll(dst)
					replaced++
				}
			} else {
				restored++
			}
			if err := os.Rename(src, dst); err != nil {
				fmt.Fprintf(lf, "[error] 搬移 %s → %s 失败: %v\n", src, dst, err)
				skipped++
			}
		}
		_ = os.RemoveAll(tmp)
		finish("success", fmt.Sprintf("已还原 %d 项", len(j.Includes)-skipped),
			restored, replaced, renamed, skipped)
		return
	}
	finish("success", fmt.Sprintf("已还原 %d 项到原位置（覆盖策略：%s）", len(j.Includes), map[string]string{
		"always": "全部覆盖", "if-changed": "仅覆盖有变化", "never": "跳过已存在"}[overwrite]))
}

// ---- API：还原历史 / 停止 ----

// ---------------------------------------------------------------------------
// 历史聚合：备份（任务日志）+ 还原（config.restores）
// ---------------------------------------------------------------------------

// histItem：历史列表里的一条（备份或还原）
type histItem struct {
	Kind     string    `json:"kind"` // backup | restore
	ID       string    `json:"id"`
	TaskID   string    `json:"task_id,omitempty"`
	TaskName string    `json:"task_name"`
	Time     string    `json:"time"` // 2006-01-02 15:04
	Status   string    `json:"status"`
	OneLine  string    `json:"one_line"`
	File     string    `json:"file,omitempty"`     // 备份：日志文件名
	Snapshot string    `json:"snapshot,omitempty"` // 还原：来源快照
	Dest     string    `json:"dest,omitempty"`
	Target   string    `json:"target,omitempty"`
	Conflict string    `json:"conflict,omitempty"`
	Includes []string  `json:"includes,omitempty"`
	Message  string    `json:"message,omitempty"`
	Pct      float64   `json:"pct,omitempty"`
	Nodes    []logNode `json:"nodes,omitempty"`
}

type histCacheEntry struct {
	mod  int64
	size int64
	sum  logSummary
}

var (
	histMu    sync.Mutex
	histCache = map[string]histCacheEntry{}
)

// readLogForHistory：大日志只取头尾，避免为了列表把几十 MB 全读进来。
// 头里有开始时间/扫描节点，尾里有 summary 和结束信息。
func readLogForHistory(fp string) ([]byte, error) {
	f, err := os.Open(fp)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const headN = 96 << 10
	const tailN = 320 << 10
	size := st.Size()
	if size <= headN+tailN {
		return io.ReadAll(f)
	}
	head := make([]byte, headN)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, err
	}
	if _, err := f.Seek(size-tailN, io.SeekStart); err != nil {
		return nil, err
	}
	tail := make([]byte, tailN)
	if _, err := io.ReadFull(f, tail); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(head)+len(tail)+2)
	out = append(out, head...)
	out = append(out, '\n')
	out = append(out, tail...)
	return out, nil
}

// parseAnyTime：兼容 RFC3339 与 "2006-01-02 15:04:05" 两种写法
func parseAnyTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, l := range []string{"2006-01-02 15:04:05", time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// historyHandler：/api/history?limit=60
func historyHandler(w http.ResponseWriter, r *http.Request) {
	limit := 80
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 300 {
			limit = n
		}
	}
	perTask := 8
	cfgMu.Lock()
	tasks := make([]Task, len(cfg.Tasks))
	copy(tasks, cfg.Tasks)
	restores := make([]RestoreJob, len(cfg.Restores))
	copy(restores, cfg.Restores)
	cfgMu.Unlock()

	items := make([]histItem, 0, limit)

	// ---- 备份：每个任务取最近 N 份日志 ----
	for _, t := range tasks {
		dir := filepath.Join(logDir(), t.ID)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		if len(names) > perTask {
			names = names[:perTask]
		}
		for _, nm := range names {
			fp := filepath.Join(dir, nm)
			fi, err := os.Stat(fp)
			if err != nil {
				continue
			}
			histMu.Lock()
			ce, ok := histCache[fp]
			histMu.Unlock()
			var sum logSummary
			if ok && ce.mod == fi.ModTime().UnixNano() && ce.size == fi.Size() {
				sum = ce.sum
			} else {
				data, err := readLogForHistory(fp)
				if err != nil {
					continue
				}
				var ft time.Time
				if tt, e2 := time.ParseInLocation("2006-01-02_150405", strings.TrimSuffix(nm, ".log"), time.Local); e2 == nil {
					ft = tt
				}
				stateMu.Lock()
				isRun := running == t.ID && runLog == fp
				stateMu.Unlock()
				sum = parseLogSummary(data, isRun, ft)
				histMu.Lock()
				if len(histCache) > 400 {
					histCache = map[string]histCacheEntry{}
				}
				histCache[fp] = histCacheEntry{mod: fi.ModTime().UnixNano(), size: fi.Size(), sum: sum}
				histMu.Unlock()
			}
			ts := ""
			if tt, e2 := time.ParseInLocation("2006-01-02_150405", strings.TrimSuffix(nm, ".log"), time.Local); e2 == nil {
				ts = tt.Format("2006-01-02 15:04")
			}
			items = append(items, histItem{
				Kind: "backup", ID: t.ID + "|" + nm, TaskID: t.ID, TaskName: t.Name,
				Time: ts, Status: sum.Status, OneLine: sum.OneLine, File: nm, Nodes: sum.Nodes,
			})
		}
	}

	// ---- 还原 ----
	for _, j := range restores {
		parts := []string{}
		if j.Status == "running" {
			parts = append(parts, fmt.Sprintf("已还原 %d%%", int(j.Pct)))
		} else {
			if j.Files > 0 {
				parts = append(parts, formatInt(j.Files)+" 个文件")
			}
			if j.Bytes > 0 {
				parts = append(parts, humanBytes(j.Bytes))
			}
			if a, e1 := parseAnyTime(j.StartedAt); e1 {
				if b, e2 := parseAnyTime(j.FinishedAt); e2 {
					if d := b.Sub(a).Seconds(); d > 0 {
						parts = append(parts, "用时 "+humanDur(d))
					}
				}
			}
			if len(parts) == 0 && j.Message != "" {
				parts = append(parts, j.Message)
			}
		}
		oneLine := strings.Join(parts, " · ")
		if j.Status == "failed" && j.Message != "" {
			oneLine = j.Message
		}
		if j.Status == "stopped" && oneLine == "" {
			oneLine = "已停止"
		}
		ts := ""
		if a, e := parseAnyTime(j.StartedAt); e {
			ts = a.Format("2006-01-02 15:04")
		}
		items = append(items, histItem{
			Kind: "restore", ID: j.ID, TaskID: j.TaskID, TaskName: j.TaskName,
			Time: ts, Status: j.Status, OneLine: oneLine,
			Snapshot: j.Snapshot, Dest: j.Dest, Target: j.Target, Conflict: j.Conflict,
			Includes: j.Includes, Message: j.Message, Pct: j.Pct,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Time > items[j].Time })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func restoresHandler(w http.ResponseWriter, r *http.Request) {
	cfgMu.Lock()
	rs := make([]RestoreJob, len(cfg.Restores))
	copy(rs, cfg.Restores)
	cfgMu.Unlock()
	writeJSON(w, 200, map[string]any{"restores": rs})
}

func restoreSubHandler(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "")
	idx := strings.LastIndex(p, "/api/restores/")
	if idx < 0 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	rid := strings.TrimSuffix(p[idx+len("/api/restores/"):], "/stop")
	if strings.HasSuffix(r.URL.Path, "/stop") {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		rstMu.Lock()
		ch := rstStops[rid]
		rstMu.Unlock()
		if ch == nil {
			writeJSON(w, 409, map[string]string{"error": "该还原当前未在运行"})
			return
		}
		close(ch)
		writeJSON(w, 200, map[string]string{"ok": "true"})
		return
	}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}

func registerRoutes(mux *http.ServeMux, base string) {
	mux.HandleFunc(base+"/api/status", statusHandler(base))
	mux.HandleFunc(base+"/api/config", configHandler)
	mux.HandleFunc(base+"/api/progress", progressHandler)
	mux.HandleFunc(base+"/api/browse", browseHandler)
	mux.HandleFunc(base+"/api/browse-cloud", browseCloudHandler)
	mux.HandleFunc(base+"/api/mkdir-cloud", mkdirCloudHandler)
	mux.HandleFunc(base+"/api/test-dav", testDavHandler)
	mux.HandleFunc(base+"/api/repos", reposHandler)
	mux.HandleFunc(base+"/api/repos/verify", repoVerifyHandler) // 防 base=="" 时歧义
	mux.HandleFunc(base+"/api/repos/summary", repoSummaryHandler)
	mux.HandleFunc(base+"/api/repos/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/verify") {
			repoVerifyHandler(w, r)
			return
		}
		// 仓库维度快照列表（仓库 Tab）
		if strings.HasSuffix(r.URL.Path, "/snapshots") && r.Method == http.MethodGet {
			repoSnapshotsHandler(w, r)
			return
		}
		// 查看仓库密码（前端下拉 👁 / 复制用）
		if strings.HasSuffix(r.URL.Path, "/password") && r.Method == http.MethodGet {
			id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/repos/"), "/password")
			cfgMu.Lock()
			rp := findRepo(id)
			var pw string
			if rp != nil {
				pw = rp.Password
			}
			cfgMu.Unlock()
			if rp == nil {
				writeJSON(w, 404, map[string]string{"error": "仓库不存在"})
				return
			}
			writeJSON(w, 200, map[string]string{"password": pw})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/delete") && r.Method == http.MethodPost {
			id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/repos/"), "/delete")
			writeJSON(w, 200, map[string]string{"ok": "true", "err": errStr(deleteRepoByID(id))})
			return
		}
		reposHandler(w, r)
	})
	mux.HandleFunc(base+"/api/tasks", tasksHandler) // 无尾斜杠：避免 301 重定向把 POST 变 GET
	mux.HandleFunc(base+"/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/run") {
			taskRunHandler(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/stop") {
			taskStopHandler(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/delete") {
			taskDeleteHandler(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, base+"/api/tasks/")
		parts := strings.Split(strings.Trim(p, "/"), "/")
		if len(parts) == 2 && parts[1] == "runs" {
			taskRunsHandler(w, r)
			return
		}
		if len(parts) == 2 && parts[1] == "log" {
			taskLogHandler(w, r)
			return
		}
		if len(parts) == 3 && parts[1] == "log" && parts[2] == "summary" {
			taskLogSummaryHandler(w, r)
			return
		}
		if len(parts) == 2 {
			switch parts[1] {
			case "snapshots":
				taskSnapshotsHandler(w, r)
				return
			case "files":
				taskFilesHandler(w, r)
				return
			case "snapdiff":
				taskSnapDiffHandler(w, r)
				return
			case "restore":
				taskRestoreHandler(w, r)
				return
			}
		}
		tasksHandler(w, r)
	})
	mux.HandleFunc(base+"/api/history", historyHandler)
	mux.HandleFunc(base+"/api/restores", restoresHandler)
	mux.HandleFunc(base+"/api/restores/", restoreSubHandler)
	mux.HandleFunc(base+"/api/hello", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"msg": "加密备份后端已就绪", "version": appVersion})
	})
	// scope：轻量接口，只回当前可见范围，供前端定时比对授权目录有没有变
	// （不要用 /api/status 做这件事，那个接口会 exec restic/rclone 取版本）
	mux.HandleFunc(base+"/api/scope", func(w http.ResponseWriter, r *http.Request) {
		sc := newScope(r)
		writeJSON(w, 200, map[string]any{
			"roots":      sc.Display,
			"home":       sc.Home,
			"source":     sc.Source,
			"authorized": sc.Authorized,
			"live":       authLive(),
			"synced_at":  authSyncedAt(),
		})
	})
	// whoami：诊断用，回显网关下发的身份 Header 与据此判定的可访问范围
	mux.HandleFunc(base+"/api/whoami", func(w http.ResponseWriter, r *http.Request) {
		sc := newScope(r)
		hdr := map[string]string{}
		for _, k := range []string{"X-Trim-Userid", "X-Trim-Username", "X-Trim-Isadmin"} {
			hdr[k] = r.Header.Get(k)
		}
		writeJSON(w, 200, map[string]any{
			"socket_mode":          socketMode(),
			"headers":              hdr,
			"uid":                  sc.ID.UID,
			"name":                 sc.ID.Name,
			"admin":                sc.ID.Admin,
			"identified":           sc.ID.OK,
			"source":               sc.Source,
			"roots":                sc.Display,
			"authorized":           sc.Authorized,
			"env_accessible_paths": os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS"),
			"scope_live":           authLive(),
			"scope_synced_at":      authSyncedAt(),
			"scope_error":          authErrText(),
		})
	})
	// reload-scope：默认走「热刷新」——直接重查平台授权表更新内存，不动进程。
	// 带 ?restart=1 时退回旧行为（应用自己 停止→启动），只在热刷新不可用时用。
	mux.HandleFunc(base+"/api/reload-scope", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"error": "只支持 POST"})
			return
		}
		// 只有管理员可以重新加载授权目录；没有身份（本机直连）时放行
		if id := identityFrom(r); id.OK && !id.Admin {
			writeJSON(w, 403, map[string]any{"error": "只有管理员可以重新加载授权目录"})
			return
		}
		if r.URL.Query().Get("restart") != "1" {
			changed, err := refreshAuthorizedPaths()
			if err == nil {
				// 立刻把生效范围回给前端，省一次往返
				sc := newScope(r)
				writeJSON(w, 200, map[string]any{
					"ok": true, "live": true, "changed": changed,
					"authorized": sc.Authorized, "source": sc.Source,
					"roots": sc.Display, "home": sc.Home,
					"msg": "授权目录已重新读取",
				})
				return
			}
			writeJSON(w, 200, map[string]any{
				"ok": true, "live": false, "error": err.Error(),
				"msg": "无法直接读取授权表，请到应用中心重启应用后生效",
			})
			return
		}
		const cli = "/usr/local/bin/appcenter-cli"
		if _, err := os.Stat(cli); err != nil {
			writeJSON(w, 400, map[string]any{"error": "当前环境不支持重启（未找到 appcenter-cli），请到应用中心重启应用"})
			return
		}
		app := appName()
		// 独立会话启动，避免被随后到来的停止信号一起带走
		script := fmt.Sprintf("sleep 1; %s stop %s; sleep 2; %s start %s", cli, app, cli, app)
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if lf, err := os.OpenFile("/tmp/encbackup-reload.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			cmd.Stdout, cmd.Stderr = lf, lf
		}
		if err := cmd.Start(); err != nil {
			writeJSON(w, 500, map[string]any{"error": "启动重启失败：" + err.Error()})
			return
		}
		go func() { _ = cmd.Wait() }()
		writeJSON(w, 200, map[string]any{"ok": true, "app": app, "msg": "应用正在重启，约 5 秒后自动恢复"})
	})
}

// basicAuth：Docker/裸端口形态的简单密码保护。
// AUTH_PASS 非空时启用 HTTP Basic Auth（constant-time 比较），为空则不鉴权（fpk Unix Socket 形态由网关保护）。
func basicAuth(next http.Handler) http.Handler {
	user := env("AUTH_USER", "admin")
	pass := os.Getenv("AUTH_PASS")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pass == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="encbackup"`)
			writeJSON(w, 401, map[string]string{"error": "需要登录"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	_ = os.MkdirAll(varDir(), 0o755)
	_ = os.MkdirAll(filepath.Join(varDir(), "cache"), 0o755)
	_ = os.MkdirAll(logDir(), 0o755)
	loadConfig()

	// 注意：分发包内不预置任何 rclone 远端/仓库（避免泄露开发者环境配置）。
	// 首次使用请在 Web UI「云端仓库」中自行添加。
	rcPath := filepath.Join(varDir(), "rclone.conf")
	_ = rcPath

	gateway := env("GATEWAY_PREFIX", "/app/encbackup")
	prefix := strings.TrimSuffix(gateway, "/")

	mux := http.NewServeMux()
	registerRoutes(mux, "")
	registerRoutes(mux, prefix)

	// 静态 UI
	staticDir := filepath.Join(filepath.Dir(os.Args[0]), "public")
	fs := http.FileServer(http.Dir(staticDir))
	serveStatic := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := os.Stat(filepath.Join(staticDir, filepath.Clean(r.URL.Path))); err != nil {
				r.URL.Path = "/"
			}
		}
		// 缓存策略：
		//  - /assets/* 是带内容哈希的文件名，改了名字就会变，可以放心长期缓存；
		//  - index.html / SPA 回落必须每次回源，否则应用升级后浏览器还在跑旧前端，
		//    表现为「明明装了新版本，界面却还是老的」。
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		fs.ServeHTTP(w, r)
	}
	mux.Handle(prefix+"/", http.StripPrefix(prefix, http.HandlerFunc(serveStatic)))
	mux.HandleFunc("/", http.HandlerFunc(serveStatic))

	go scheduler()
	// 授权目录热更新：管理员在应用中心改完勾选，几秒内自动生效，无需重启
	go authWatcher()

	socketPath := os.Getenv("SOCKET_PATH")
	if socketPath != "" {
		_ = os.Remove(socketPath)
		l, err := net.Listen("unix", socketPath)
		if err != nil {
			panic("listen unix: " + err.Error())
		}
		// socket 权限：默认只允许网关（nginx worker，www-data 组）与 root 访问，
		// 避免本机其它用户绕过飞牛统一鉴权直连 API。组不存在时退回 0666 保证可用。
		if err := os.Chmod(socketPath, 0o660); err == nil {
			if g, gerr := user.LookupGroup("www-data"); gerr == nil {
				if gid, aerr := strconv.Atoi(g.Gid); aerr == nil {
					_ = os.Chown(socketPath, 0, gid)
				} else {
					_ = os.Chmod(socketPath, 0o666)
				}
			} else {
				_ = os.Chmod(socketPath, 0o666)
			}
		}
		if err := http.Serve(l, mux); err != nil {
			panic(err)
		}
		return
	}

	port := env("PORT", "5175")
	host := env("HOST", "localhost")
	_ = http.ListenAndServe(host+":"+port, basicAuth(mux))
}
