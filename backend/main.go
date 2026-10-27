// 加密备份 - 后端服务
// 通过飞牛统一网关（Unix Socket）对外提供 API 与静态 UI。
// 功能：restic 备份任务管理、内置调度器、进度解析、日志、仓库管理。
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const appVersion = "0.2.0"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func varDir() string { return env("TRIM_PKGVAR", "./var") }

// userRoot：用户视角根目录（默认飞牛个人主目录，Docker 部署可用 USER_ROOT 覆盖）
func userRoot() string { return env("USER_ROOT", "/vol1/1000") }

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
	Remote    string `json:"remote,omitempty"`    // rclone 远端名（wd-xxxxxxxx）
	DavURL    string `json:"dav_url,omitempty"`   // 服务器地址
	DavUser   string `json:"dav_user,omitempty"`  // 账号
	DavPass   string `json:"dav_pass,omitempty"`  // 密码（输出时掩码）
	DavVendor string `json:"dav_vendor,omitempty"` // other / owncloud / nextcloud / sharepoint
	Inited    bool   `json:"inited,omitempty"`   // restic init 已成功完成
}

type Task struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Paths        []string `json:"paths"`
	Excludes     []string `json:"excludes"`
	RepoID       string   `json:"repo_id"`
	DestCloud    string   `json:"dest_cloud,omitempty"`   // 目的地两步式：tianyi / y115
	DestSubdir   string   `json:"dest_subdir,omitempty"` // 云端目标目录
	SchedMode    string   `json:"sched_mode"` // manual | daily | weekly | monthly | interval
	DailyTime    string   `json:"daily_time"` // 执行时间 "03:00"（daily/weekly/monthly 共用）
	IntervalHrs  int      `json:"interval_hrs"`
	DailyDays    int      `json:"daily_days"`   // daily: 每 N 天（0/1 = 每天）
	WeeklyDays   []int    `json:"weekly_days"`  // weekly: 0=周日 … 6=周六
	MonthlyDays  []int    `json:"monthly_days"` // monthly: 1-31，-1=末天
	KeepLast     int      `json:"keep_last"` // restic forget --keep-last, 0=不清理
	Enabled      bool     `json:"enabled"`
	CreatedAt    string   `json:"created_at"`
	LastRunAt    string   `json:"last_run_at"`
	LastDoneAt   string   `json:"last_done_at"` // 上次成功结束时间（调度去重用）
	LastStatus   string   `json:"last_status"`  // success | failed | running | ""
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
	Snapshot     string   `json:"snapshot"`            // restic 快照 ID（短）
	SnapshotTime string   `json:"snapshot_time,omitempty"`
	Dest         string   `json:"dest"`               // original | custom
	Target       string   `json:"target,omitempty"`   // custom：目标目录
	Conflict     string   `json:"conflict"`           // always | if-changed | never | rename
	Includes     []string `json:"includes"`           // 要还原的绝对路径
	Status       string   `json:"status"`             // running | success | failed | stopped
	Message      string   `json:"message,omitempty"`
	StartedAt    string   `json:"started_at"`
	FinishedAt   string   `json:"finished_at,omitempty"`
	Pct          float64  `json:"pct"`
	Files        int64    `json:"files"`
	Bytes        int64    `json:"bytes"`
	Restored     int      `json:"restored"`          // 新增（原不存在）
	Replaced     int      `json:"replaced"`          // 覆盖
	Renamed      int      `json:"renamed"`           // 保留两者（重命名）
	Skipped      int      `json:"skipped"`           // 跳过
}

// ---------------------------------------------------------------------------
// 全局状态
// ---------------------------------------------------------------------------

var (
	cfgMu    sync.Mutex
	cfg      = &Config{}
	stateMu  sync.Mutex
	running  = ""    // 正在运行的任务 ID
	runStop  chan struct{} // 停止信号
	runStart string // 当前运行开始时间（RFC3339）
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
	authMu   sync.Mutex
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

	probeName := "_auth_probe_" + randomID()
	cmd := exec.Command(binPath("rclone"), "rcat", remote+"/"+probeName,
		"--contimeout", "10s", "--timeout", "20s")
	cmd.Env = rcloneEnv()
	cmd.Stdin = strings.NewReader("probe")
	out, err := cmd.CombinedOutput()
	ok := err == nil
	if ok {
		// 探测成功，清理探测文件（失败不阻塞结果）
		_ = exec.Command(binPath("rclone"), "deletefile", remote+"/"+probeName,
			"--contimeout", "10s", "--timeout", "20s").Run()
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

// findOrCreateRepoLocked：按 云盘+目录 查找仓库，不存在则自动创建（随机密码）。
// cloudKey 支持两种：tianyi/y115（ol 中转），或 WebDAV 仓库的远端名 wd-xxxxxxxx。
// 调用方需持有 cfgMu。返回（仓库，是否新建，错误）。
func findOrCreateRepoLocked(cloudKey, subdir string) (*Repo, bool, error) {
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
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	seg := sub
	if idx := strings.LastIndex(seg, "/"); idx >= 0 {
		seg = seg[idx+1:]
	}
	nr := Repo{ID: "repo-" + randomID(), Name: displayName, URI: uri, Password: hex.EncodeToString(b), Cloud: "webdav", Remote: cloudKey}
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
	MessageType string   `json:"message_type"`
	PercentDone float64  `json:"percent_done"`
	TotalFiles  int64    `json:"total_files"`
	FilesDone   int64    `json:"files_done"`
	TotalBytes  int64    `json:"total_bytes"`
	BytesDone   int64    `json:"bytes_done"`
	CurrentFiles []string `json:"current_files"`
	ErrorCount  int64    `json:"error_count"`
	SnapshotID  string   `json:"snapshot_id"`
	TotalFilesProcessed int64 `json:"total_files_processed"`
	TotalBytesProcessed int64 `json:"total_bytes_processed"`
	SecondsElapsed float64 `json:"seconds_elapsed"`
	TotalDuration  float64 `json:"total_duration"`
	BackupStart    string  `json:"backup_start"`
	BackupEnd      string  `json:"backup_end"`
	FilesNew       int64   `json:"files_new"`
	FilesChanged   int64   `json:"files_changed"`
	FilesUnmodified int64  `json:"files_unmodified"`
	Code           int64   `json:"code"`
	Message        string  `json:"message"`
}

type runResult struct {
	SnapshotID string
	Files      int64
	Bytes      int64
	Err        error
	ErrOutput  string
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
	if res.Err != nil {
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
		writeJSON(w, 200, map[string]any{
			"app":            "encbackup",
			"version":        appVersion,
			"gateway":        gateway,
		"socket_mode":    os.Getenv("SOCKET_PATH") != "",
		"data_dir":       varDir(),
		"user_root":      userRoot(),
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
				nr.Name = "WebDAV·" + sub
			}
		}
		if nr.URI == "" {
			writeJSON(w, 400, map[string]string{"error": "参数不完整"})
			return
		}
		isNew := false
		cfgMu.Lock()
		if nr.ID == "" {
			nr.ID = "repo-" + randomID()
		}
		if old := findRepo(nr.ID); old != nil {
			nr = unmaskPassword(nr, old)
			*old = nr
		} else {
			isNew = true
			// restic 仓库密码：新建未提供则自动生成（API/Docker 部署友好）
			if nr.Password == "" {
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
		// 新仓库自动 restic init（目录冲突时回滚并报错）
		if isNew {
			logPath := filepath.Join(varDir(), "init-"+nr.ID+".log")
			lf, _ := os.Create(logPath)
			ires := runRestic(&nr, []string{"init"}, lf, nil, false)
			lf.Close()
			if ires.Err != nil {
				hint := initFailHint(logPath)
				if strings.Contains(hint, "已存在一个加密仓库") {
					_ = deleteRepoByID(nr.ID)
					writeJSON(w, 400, map[string]string{"error": hint})
					return
				}
			} else {
				cfgMu.Lock()
				if lr := findRepo(nr.ID); lr != nil {
					lr.Inited = true
				}
				_ = saveConfig()
				cfgMu.Unlock()
			}
		}
		writeJSON(w, 200, map[string]string{"id": nr.ID})
	case http.MethodDelete:
		writeJSON(w, 200, map[string]string{"ok": "true", "err": errStr(deleteRepoByID(id))})
	default:
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
	}
}

// deleteRepoByID：删除仓库（DELETE /api/repos/{id} 与 POST /api/repos/{id}/delete 共用）
func deleteRepoByID(id string) error {
	if id == "" {
		return fmt.Errorf("缺少 id")
	}
	cfgMu.Lock()
	defer cfgMu.Unlock()
	for i := range cfg.Repos {
		if cfg.Repos[i].ID == id {
			cfg.Repos = append(cfg.Repos[:i], cfg.Repos[i+1:]...)
			break
		}
	}
	return saveConfig()
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
			rp, created, ferr := findOrCreateRepoLocked(nt.DestCloud, nt.DestSubdir)
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
		// 新建仓库时自动 restic init（失败不阻塞建任务，首次备份会再报）
		initErr := ""
		if newRepo != nil {
			logPath := filepath.Join(varDir(), "init-"+newRepo.ID+".log")
			lf, _ := os.Create(logPath)
			ires := runRestic(newRepo, []string{"init"}, lf, nil, false)
			lf.Close()
			if ires.Err != nil {
				hint := initFailHint(logPath)
				if strings.Contains(hint, "已存在一个加密仓库") {
					// 目录冲突：回滚刚建的仓库（密码与已有仓库不一致，留着也无法用），让用户重新选目录
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
	Status   string    `json:"status"`    // success | failed | running
	Title    string    `json:"title"`     // 如 "10-26 17:00 的备份"
	OneLine  string    `json:"one_line"`  // 一行结果："12,846 个文件 · 13.5 GB · 用时 18 分"
	Percent  float64   `json:"percent"`   // 运行中时的进度（0-100）
	Speed    string    `json:"speed"`     // 运行中时的速度（可选）
	Nodes    []logNode `json:"nodes"`     // 关键节点时间轴
	ErrDetail string   `json:"err_detail"` // 失败时的原始错误详情（可折叠）
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
	root := userRoot()
	if req.Path == "" {
		req.Path = root
	}
	entries, err := os.ReadDir(req.Path)
	if err != nil {
		writeJSON(w, 200, map[string]any{"path": req.Path, "dirs": []any{}, "user_root": root, "error": err.Error()})
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
		if req.Path == "/vol1" && name != "1000" {
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
	writeJSON(w, 200, map[string]any{"path": req.Path, "dirs": dirs, "user_root": root})
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
	}
	_ = readBody(r, &req)
	if req.Remote == "" && req.Cloud == "" {
		writeJSON(w, 400, map[string]string{"error": "missing cloud"})
		return
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
	cmd := exec.Command(binPath("rclone"), "lsf", remote, "--dirs-only")
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
	}
	_ = readBody(r, &req)
	remote := req.Remote
	if remote == "" {
		remote = "ol:" + req.Cloud
	}
	if remote == "ol:" && strings.Trim(req.Path, "/") == "" {
		writeJSON(w, 400, map[string]string{"error": "missing cloud or path"})
		return
	}
	if !strings.Contains(remote, ":") {
		remote += ":" // 纯远端名（WebDAV）必须带冒号，否则 rclone 当本地路径
	}
	remote = remote + "/" + strings.Trim(req.Path, "/")
	cmd := exec.Command(binPath("rclone"), "mkdir", remote)
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

// ---- 快照文件列表缓存（LRU 2 份，含目录树全量节点） ----

type snapNode struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
}

type snapListing struct {
	Roots  []string              // 快照的源路径（绝对）
	Nodes  []snapNode            // 全部节点（目录+文件）
	byPath map[string]*snapNode  `json:"-"`
	Files  []snapNode            // 仅文件（搜索用）
	total  int
}

var (
	lsMu    sync.Mutex
	lsCache = map[string]*snapListing{} // key: repoID|snapshotID
	lsOrder []string
)

func getSnapshotListing(repo *Repo, snapID string) (*snapListing, error) {
	key := repo.ID + "|" + snapID
	lsMu.Lock()
	if sl := lsCache[key]; sl != nil {
		lsMu.Unlock()
		return sl, nil
	}
	lsMu.Unlock()

	lp := filepath.Join(logDir(), "ls-"+snapID+".log")
	out, err := runResticCapture(repo, []string{"ls", "--json", snapID}, lp)
	if err != nil {
		return nil, fmt.Errorf("读取快照文件列表失败（详见日志 %s）: %v", lp, err)
	}
	sl := &snapListing{byPath: map[string]*snapNode{}}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
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
		var n snapNode
		if json.Unmarshal([]byte(line), &n) != nil || n.Path == "" {
			continue
		}
		sl.Nodes = append(sl.Nodes, n)
		sl.byPath[n.Path] = &sl.Nodes[len(sl.Nodes)-1]
		if n.Type != "dir" {
			sl.Files = append(sl.Files, n)
		}
	}
	sl.total = len(sl.Files)

	lsMu.Lock()
	if _, ok := lsCache[key]; !ok {
		lsCache[key] = sl
		lsOrder = append(lsOrder, key)
		for len(lsOrder) > 2 {
			delete(lsCache, lsOrder[0])
			lsOrder = lsOrder[1:]
		}
	}
	kept := lsCache[key]
	lsMu.Unlock()
	return kept, nil
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

// ---- API：文件浏览 / 搜索 ----

func taskFilesHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(afterMarker(r.URL.Path, "/api/tasks/"), "/files")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	snapID := strings.TrimSpace(r.URL.Query().Get("snapshot"))
	dir := strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("dir")), "/")
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
	sl, err := getSnapshotListing(&rp, snapID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
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
		parent := filepath.Dir(dir)
		for i := range sl.Nodes {
			nd := &sl.Nodes[i]
			if nd.Path != dir && filepath.Dir(nd.Path) == dir {
				addNode(nd, parent)
			}
		}
		// 目录在前、按名排序
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
	writeJSON(w, 200, map[string]any{"dir": dir, "q": q, "items": items, "total_files": sl.total, "roots": sl.Roots})
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
	mux.HandleFunc(base+"/api/repos/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/verify") {
			repoVerifyHandler(w, r)
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
	mux.HandleFunc(base+"/api/restores", restoresHandler)
	mux.HandleFunc(base+"/api/restores/", restoreSubHandler)
	mux.HandleFunc(base+"/api/hello", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"msg": "加密备份后端已就绪", "version": appVersion})
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
		fs.ServeHTTP(w, r)
	}
	mux.Handle(prefix+"/", http.StripPrefix(prefix, http.HandlerFunc(serveStatic)))
	mux.HandleFunc("/", http.HandlerFunc(serveStatic))

	go scheduler()

	socketPath := os.Getenv("SOCKET_PATH")
	if socketPath != "" {
		_ = os.Remove(socketPath)
		l, err := net.Listen("unix", socketPath)
		if err != nil {
			panic("listen unix: " + err.Error())
		}
		_ = os.Chmod(socketPath, 0o666)
		if err := http.Serve(l, mux); err != nil {
			panic(err)
		}
		return
	}

	port := env("PORT", "5175")
	host := env("HOST", "localhost")
	_ = http.ListenAndServe(host+":"+port, basicAuth(mux))
}
