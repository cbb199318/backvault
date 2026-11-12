package main

// 应用内热更新：不依赖飞牛应用商店的审核周期，从 GitHub Releases（及国内镜像）拉取新版本。
//
// 目录布局（全部在 TRIM_PKGVAR 下，商店升级不会覆盖）：
//
//	overlay/
//	  0.4.6/encbackup-server   新版后端
//	  0.4.6/public/            新版前端
//	  0.4.6/VERSION
//	  current  -> 0.4.6        当前生效版本（cmd/main 启动时优先用它）
//	  previous -> 0.4.5        回滚目标；不存在表示回滚到商店内置版本
//	  trial                    试运行中（内容=版本号）：新版本稳定运行 15 秒后自己删除
//	  trial.booted             cmd/main 已经用试运行版本启动过一次
//	  failed                   上次自动回滚掉的版本号（给界面提示用）
//	  config.pre-<ver>.json    切换前的配置快照，回滚时还原
//
// 安全：latest.json 必须带 ed25519 签名（latest.json.sig），公钥编译进二进制
// （update_pubkey.go）。安装包的 sha256 写在已签名的 latest.json 里，所以镜像站
// 只是搬运工，篡改任何字节都会校验失败。
//
// 跨版本：每次都下载最新的完整包，不做增量；数据形状的变化由 migrate.go 在启动时
// 一次性按顺序迁移。latest.json 的 min_from / min_store_version 用来拦住太老、
// 必须先走商店升级的安装。
//
// 兼容承诺：latest.json 的地址与字段只增不删，否则已经装出去的老版本会失联。

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	updateRepo        = "cbb199318/backvault"
	updateManifestURL = "https://github.com/" + updateRepo + "/releases/latest/download/latest.json"
	maxPackageBytes   = 200 << 20 // 安装包上限，防止被喂超大文件
	trialConfirmAfter = 15 * time.Second
	autoCheckEvery    = 12 * time.Hour
)

// GitHub 国内直连不稳定：同一个地址依次尝试直连和镜像前缀。签名保证镜像无法篡改内容。
var defaultMirrorPrefixes = []string{"", "https://ghfast.top/", "https://gh-proxy.com/"}

type updAsset struct {
	Name   string   `json:"name"`
	URLs   []string `json:"urls"`
	SHA256 string   `json:"sha256"`
	Size   int64    `json:"size"`
}

type updNote struct {
	Version string   `json:"version"`
	Date    string   `json:"date,omitempty"`
	Items   []string `json:"items"`
}

type updManifest struct {
	Version         string              `json:"version"`
	MinFrom         string              `json:"min_from,omitempty"`          // 低于此版本不能热更新（数据或更新器太老）
	MinStoreVersion string              `json:"min_store_version,omitempty"` // 商店内置版本低于此值时需先到商店升级（启动脚本/权限变了）
	PublishedAt     string              `json:"published_at,omitempty"`
	Notes           []updNote           `json:"notes,omitempty"` // 新 → 旧，最近若干个版本
	Assets          map[string]updAsset `json:"assets"`          // key: linux-amd64 / linux-arm64
}

type updStatus struct {
	Enabled     bool      `json:"enabled"`
	DisabledWhy string    `json:"disabled_reason,omitempty"`
	Current     string    `json:"current"`
	Builtin     string    `json:"builtin"`
	FromOverlay bool      `json:"from_overlay"` // 当前跑的是热更新版本（而不是商店内置版本）
	CheckedAt   string    `json:"checked_at,omitempty"`
	CheckErr    string    `json:"check_error,omitempty"`
	Latest      string    `json:"latest,omitempty"`
	Available   bool      `json:"available"`         // 有新版本且可以直接热更新
	NeedStore   bool      `json:"need_store"`        // 有新版本，但必须先去商店升级
	Blocked     string    `json:"blocked,omitempty"` // 暂时不能更新的原因（如任务运行中）
	Notes       []updNote `json:"notes,omitempty"`   // 只含比当前新的版本，合并展示
	Size        int64     `json:"size,omitempty"`
	Phase       string    `json:"phase"` // idle | checking | downloading | installing | restarting | error
	Progress    float64   `json:"progress"`
	Error       string    `json:"error,omitempty"`
	Failed      string    `json:"failed,omitempty"` // 上次自动回滚掉的版本
}

var (
	updMu       sync.Mutex
	updLatest   *updManifest
	updChecked  time.Time
	updCheckErr string
	updPhase    = "idle"
	updProgress float64
	updErr      string
)

func overlayRoot() string { return filepath.Join(varDir(), "overlay") }

// builtinVersion 商店安装的版本（cmd/main 通过环境变量传入）；直连开发时等于当前版本
func builtinVersion() string {
	if v := strings.TrimSpace(os.Getenv("BUILTIN_VERSION")); v != "" {
		return v
	}
	return appVersion
}

func updateEnabled() (bool, string) {
	if os.Getenv("UPDATE_DISABLED") == "1" {
		return false, "已通过 UPDATE_DISABLED 关闭"
	}
	if updatePubKey == "" {
		return false, "此构建未内置更新公钥"
	}
	// Docker 形态应该换镜像升级，不在容器里改二进制
	if !socketMode() && os.Getenv("UPDATE_ENABLED") != "1" {
		return false, "仅飞牛应用形态支持在线更新"
	}
	if runtime.GOOS != "linux" {
		return false, "仅支持 Linux"
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// 版本号
// ---------------------------------------------------------------------------

var verNumRe = regexp.MustCompile(`\d+`)

// cmpVer 比较 x.y.z（忽略前缀 v 与 -beta 之类后缀）：a<b 返回 -1
func cmpVer(a, b string) int {
	pa := verNumRe.FindAllString(strings.SplitN(strings.TrimPrefix(a, "v"), "-", 2)[0], -1)
	pb := verNumRe.FindAllString(strings.SplitN(strings.TrimPrefix(b, "v"), "-", 2)[0], -1)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

var safeVerRe = regexp.MustCompile(`^\d+\.\d+\.\d+([.-][0-9A-Za-z.]+)?$`)

// ---------------------------------------------------------------------------
// 下载与校验
// ---------------------------------------------------------------------------

func mirrorPrefixes() []string {
	if v := strings.TrimSpace(os.Getenv("UPDATE_MIRRORS")); v != "" {
		out := []string{""}
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, strings.TrimSuffix(p, "/")+"/")
			}
		}
		return out
	}
	return defaultMirrorPrefixes
}

// candidates 把一组地址按「直连 → 镜像」展开；非 GitHub 地址原样保留
func candidates(urls []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, pre := range mirrorPrefixes() {
		for _, u := range urls {
			c := u
			if pre != "" {
				if !strings.HasPrefix(u, "https://github.com/") {
					continue
				}
				c = pre + u
			}
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

func manifestURLs() []string {
	if v := strings.TrimSpace(os.Getenv("UPDATE_MANIFEST_URL")); v != "" {
		return candidates(strings.Split(v, ","))
	}
	return candidates([]string{updateManifestURL})
}

func httpGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "encbackup-updater/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("响应过大")
	}
	return b, nil
}

func verifySig(data, sigB64 []byte) error {
	pub, err := base64.StdEncoding.DecodeString(updatePubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("内置公钥无效")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("签名格式不正确")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return errors.New("签名校验失败")
	}
	return nil
}

func shortHost(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.Index(s, "/"); i > 0 {
		return s[:i]
	}
	return s
}

func checkUpdate(ctx context.Context) error {
	if ok, _ := updateEnabled(); !ok {
		return nil
	}
	updMu.Lock()
	if updPhase != "idle" && updPhase != "error" {
		updMu.Unlock()
		return nil
	}
	updPhase = "checking"
	updMu.Unlock()

	m, err := fetchManifest(ctx)

	updMu.Lock()
	defer updMu.Unlock()
	updPhase = "idle"
	updChecked = time.Now()
	if err != nil {
		updCheckErr = err.Error()
		log.Printf("[update] 检查失败：%v", err)
		return err
	}
	updCheckErr = ""
	updLatest = m
	return nil
}

func autoCheckLoop() {
	if ok, _ := updateEnabled(); !ok {
		return
	}
	time.Sleep(2 * time.Minute) // 避开启动高峰，也给试运行确认留时间
	for {
		_ = checkUpdate(context.Background())
		time.Sleep(autoCheckEvery)
	}
}

// ---------------------------------------------------------------------------
// 状态
// ---------------------------------------------------------------------------

func busyReason() string {
	stateMu.Lock()
	r := running
	stateMu.Unlock()
	if r != "" {
		return "有备份任务正在运行，结束后再更新"
	}
	cfgMu.Lock()
	defer cfgMu.Unlock()
	for _, j := range cfg.Restores {
		if j.Status == "running" {
			return "有还原任务正在运行，结束后再更新"
		}
	}
	return ""
}

func readTrim(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

func currentUpdateStatus() updStatus {
	ok, why := updateEnabled()
	st := updStatus{
		Enabled:     ok,
		DisabledWhy: why,
		Current:     appVersion,
		Builtin:     builtinVersion(),
		FromOverlay: cmpVer(appVersion, builtinVersion()) > 0,
		Failed:      readTrim(filepath.Join(overlayRoot(), "failed")),
	}
	updMu.Lock()
	defer updMu.Unlock()
	st.Phase, st.Progress, st.Error, st.CheckErr = updPhase, updProgress, updErr, updCheckErr
	if !updChecked.IsZero() {
		st.CheckedAt = updChecked.Format(time.RFC3339)
	}
	m := updLatest
	if m == nil || cmpVer(m.Version, appVersion) <= 0 {
		return st
	}
	st.Latest = m.Version
	for _, n := range m.Notes {
		if cmpVer(n.Version, appVersion) > 0 {
			st.Notes = append(st.Notes, n)
		}
	}
	if st.Failed != "" && st.Failed == m.Version {
		// 同一个版本已经启动失败并回滚过：不再反复推，等下一个版本或走商店
		st.Blocked = fmt.Sprintf("v%s 上次更新后未能正常启动，已自动回滚；请等待下一个版本或通过应用商店更新", m.Version)
		return st
	}
	if a, ok := m.Assets[assetKey()]; ok {
		st.Size = a.Size
	} else {
		st.NeedStore = true
		st.Blocked = "新版本暂未提供本机架构（" + assetKey() + "）的在线更新包，请到应用商店更新"
		return st
	}
	if m.MinFrom != "" && cmpVer(appVersion, m.MinFrom) < 0 {
		st.NeedStore = true
		st.Blocked = fmt.Sprintf("当前版本过旧（需 ≥ %s 才能在线更新），请先到飞牛应用商店更新", m.MinFrom)
		return st
	}
	if m.MinStoreVersion != "" && cmpVer(builtinVersion(), m.MinStoreVersion) < 0 {
		st.NeedStore = true
		st.Blocked = fmt.Sprintf("新版本需要应用商店版本 ≥ %s（当前 %s），请先到飞牛应用商店更新", m.MinStoreVersion, builtinVersion())
		return st
	}
	st.Available = true
	return st
}

func assetKey() string { return "linux-" + runtime.GOARCH }

// ---------------------------------------------------------------------------
// 安装
// ---------------------------------------------------------------------------

func setPhase(phase string, progress float64, err string) {
	updMu.Lock()
	updPhase, updProgress, updErr = phase, progress, err
	updMu.Unlock()
}

// startApply 校验前置条件并异步执行安装；返回错误表示没有开始
func startApply() error {
	st := currentUpdateStatus()
	if !st.Enabled {
		return errors.New(st.DisabledWhy)
	}
	if !st.Available {
		if st.Blocked != "" {
			return errors.New(st.Blocked)
		}
		return errors.New("没有可更新的版本，请先检查更新")
	}
	if r := busyReason(); r != "" {
		return errors.New(r)
	}
	updMu.Lock()
	if updPhase != "idle" && updPhase != "error" {
		updMu.Unlock()
		return errors.New("更新已在进行中")
	}
	m := updLatest
	updPhase, updProgress, updErr = "downloading", 0, ""
	updMu.Unlock()

	go func() {
		if err := applyUpdate(m); err != nil {
			log.Printf("[update] 安装 %s 失败：%v", m.Version, err)
			setPhase("error", 0, err.Error())
		}
	}()
	return nil
}

// 下载实现见 update_race.go：多源同时测速，选最快的继续下载

// extractTarGz 安全解压：只接受普通文件与目录，拒绝绝对路径、.. 与链接
func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("安装包含非法路径：%s", h.Name)
		}
		target := filepath.Join(dst, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > 4*maxPackageBytes {
				return errors.New("解压后体积超过上限")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, io.LimitReader(tr, h.Size))
			out.Close()
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("安装包含不支持的条目类型：%s", h.Name)
		}
	}
}

// swapSymlink 原子地把 link 指向 target（相对路径）
func swapSymlink(target, link string) error {
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

func applyUpdate(m *updManifest) error {
	a := m.Assets[assetKey()]
	root := overlayRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	// 1. 下载 + sha256
	pkg := filepath.Join(root, ".download.tar.gz")
	defer os.Remove(pkg)
	if err := downloadAsset(a, pkg); err != nil {
		return err
	}

	// 2. 解压到临时目录并自检
	setPhase("installing", 1, "")
	stage := filepath.Join(root, "."+m.Version+".tmp")
	_ = os.RemoveAll(stage)
	if err := extractTarGz(pkg, stage); err != nil {
		os.RemoveAll(stage)
		return err
	}
	bin := filepath.Join(stage, "encbackup-server")
	if v := readVersionFile(filepath.Join(stage, "VERSION")); v != m.Version {
		os.RemoveAll(stage)
		return fmt.Errorf("安装包版本（%s）与清单（%s）不一致", v, m.Version)
	}
	if fi, err := os.Stat(filepath.Join(stage, "public", "index.html")); err != nil || fi.IsDir() {
		os.RemoveAll(stage)
		return errors.New("安装包缺少前端文件")
	}
	// 试跑一次 --version：架构不对、文件损坏在这里就会暴露，不用等重启后才发现
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	cancel()
	if err != nil || strings.TrimSpace(string(out)) != m.Version {
		os.RemoveAll(stage)
		return fmt.Errorf("新版本自检失败（%v，输出 %q）", err, strings.TrimSpace(string(out)))
	}

	final := filepath.Join(root, m.Version)
	_ = os.RemoveAll(final)
	if err := os.Rename(stage, final); err != nil {
		return err
	}

	// 3. 再确认一次没有任务刚好开始
	if r := busyReason(); r != "" {
		return errors.New(r + "（新版本已下载，稍后再点更新即可）")
	}

	// 4. 记录回滚点并切换
	cfgMu.Lock()
	_ = copyFile(filepath.Join(varDir(), "config.json"), filepath.Join(root, "config.pre-"+m.Version+".json"))
	cfgMu.Unlock()
	cur := filepath.Join(root, "current")
	prev := filepath.Join(root, "previous")
	if t, err := os.Readlink(cur); err == nil && t != m.Version {
		_ = swapSymlink(t, prev)
	} else if err != nil {
		_ = os.Remove(prev) // 没有 current：回滚目标就是商店内置版本
	}
	_ = os.Remove(filepath.Join(root, "trial.booted"))
	_ = os.Remove(filepath.Join(root, "failed"))
	if err := os.WriteFile(filepath.Join(root, "trial"), []byte(m.Version+"\n"), 0o644); err != nil {
		return err
	}
	if err := swapSymlink(m.Version, cur); err != nil {
		_ = os.Remove(filepath.Join(root, "trial"))
		return err
	}
	pruneOverlay(m.Version)

	log.Printf("[update] %s → %s 已安装，正在重启", appVersion, m.Version)
	setPhase("restarting", 1, "")
	go func() {
		time.Sleep(800 * time.Millisecond) // 让 HTTP 响应先发出去
		restartApp(filepath.Join(final, "encbackup-server"), m.Version)
	}()
	return nil
}

// pruneOverlay 只保留 keep 与 previous 指向的版本，其余删掉
func pruneOverlay(keep string) {
	root := overlayRoot()
	prev, _ := os.Readlink(filepath.Join(root, "previous"))
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || strings.HasPrefix(n, ".") || n == keep || n == prev {
			continue
		}
		if safeVerRe.MatchString(n) {
			_ = os.RemoveAll(filepath.Join(root, n))
		}
	}
	// 只保留与当前/回滚版本相关的配置快照
	files, _ := filepath.Glob(filepath.Join(root, "config.pre-*.json"))
	for _, f := range files {
		v := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "config.pre-"), ".json")
		if v != keep {
			_ = os.Remove(f)
		}
	}
}

// rollbackScript 由看门狗与 cmd/main 共用的回滚逻辑（cmd/main 里有一份同样的 shell 实现）
const rollbackSh = `rollback() {
  V="$(cat "$O/trial" 2>/dev/null)"
  if [ -L "$O/previous" ]; then ln -sfn "$(readlink "$O/previous")" "$O/current"; else rm -f "$O/current"; fi
  [ -n "$V" ] && [ -f "$O/config.pre-$V.json" ] && cp -f "$O/config.pre-$V.json" "$D/config.json"
  [ -n "$V" ] && echo "$V" > "$O/failed"
  rm -f "$O/trial" "$O/trial.booted"
  echo "$(date '+%F %T') rolled back from $V"
}
`

// restartApp 优先走 appcenter-cli（飞牛面板状态保持一致，并带回滚看门狗），否则原地 exec
func restartApp(newBin, newVer string) {
	const cli = "/usr/local/bin/appcenter-cli"
	if _, err := os.Stat(cli); err == nil && socketMode() {
		app := appName()
		var watch string
		if newVer != "" {
			// 新版本 45 秒内没把 trial 删掉（没起来或起来就崩），自动回滚到上一个版本再拉起
			watch = fmt.Sprintf(`i=0; while [ $i -lt 45 ]; do [ -f "$O/trial" ] || exit 0; sleep 1; i=$((i+1)); done
rollback; %[1]s stop %[2]s; sleep 2; %[1]s start %[2]s
`, cli, app)
		}
		script := fmt.Sprintf("O=%q; D=%q\n%ssleep 1; %s stop %s; sleep 2; %s start %s\n%s",
			overlayRoot(), varDir(), rollbackSh, cli, app, cli, app, watch)
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if lf, err := os.OpenFile(filepath.Join(varDir(), "update.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			cmd.Stdout, cmd.Stderr = lf, lf
		}
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
			return
		}
		log.Printf("[update] appcenter-cli 重启失败，改用 exec")
	}
	// 回退：原地替换进程（PID 不变，cmd/main 的 pid 文件依然有效）
	if newBin == "" {
		newBin = filepath.Join(env("TRIM_APPDEST", "."), "server", "encbackup-server")
	}
	if sp := os.Getenv("SOCKET_PATH"); sp != "" {
		_ = os.Remove(sp)
	}
	_ = os.Chdir(filepath.Dir(newBin))
	args := append([]string{newBin}, os.Args[1:]...)
	if err := syscall.Exec(newBin, args, os.Environ()); err != nil {
		log.Printf("[update] exec 失败：%v", err)
		setPhase("error", 0, "重启失败："+err.Error()+"，请到应用中心手动重启")
	}
}

// confirmTrial 新版本启动后调用：稳定运行一段时间就转正，否则交给看门狗/cmd/main 回滚
func confirmTrial() {
	root := overlayRoot()
	if readTrim(filepath.Join(root, "trial")) != appVersion {
		return
	}
	log.Printf("[update] %s 试运行中，%v 后确认", appVersion, trialConfirmAfter)
	time.Sleep(trialConfirmAfter)
	_ = os.Remove(filepath.Join(root, "trial"))
	_ = os.Remove(filepath.Join(root, "trial.booted"))
	log.Printf("[update] %s 已确认", appVersion)
}

// resetToBuiltin 放弃热更新版本，回到商店内置版本
func resetToBuiltin() error {
	if r := busyReason(); r != "" {
		return errors.New(r)
	}
	root := overlayRoot()
	for _, n := range []string{"current", "previous", "trial", "trial.booted", "failed"} {
		_ = os.Remove(filepath.Join(root, n))
	}
	pruneOverlay("")
	setPhase("restarting", 1, "")
	go func() {
		time.Sleep(800 * time.Millisecond)
		restartApp("", "")
	}()
	return nil
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if id := identityFrom(r); id.OK && !id.Admin {
		writeJSON(w, 403, map[string]any{"error": "只有管理员可以更新应用"})
		return false
	}
	return true
}

func updateHandler(w http.ResponseWriter, r *http.Request) {
	st := currentUpdateStatus()
	if st.Available || st.NeedStore {
		st.Blocked = firstNonEmpty(st.Blocked, busyReason())
	}
	writeJSON(w, 200, st)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func updateCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "只支持 POST"})
		return
	}
	if ok, why := updateEnabled(); !ok {
		writeJSON(w, 400, map[string]any{"error": why})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := checkUpdate(ctx); err != nil {
		writeJSON(w, 502, map[string]any{"error": "检查更新失败：" + err.Error()})
		return
	}
	updateHandler(w, r)
}

func updateApplyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "只支持 POST"})
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	if err := startApply(); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func updateResetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "只支持 POST"})
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	if err := resetToBuiltin(); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func registerUpdateRoutes(mux *http.ServeMux, base string) {
	mux.HandleFunc(base+"/api/update", updateHandler)
	mux.HandleFunc(base+"/api/update/check", updateCheckHandler)
	mux.HandleFunc(base+"/api/update/apply", updateApplyHandler)
	mux.HandleFunc(base+"/api/update/reset", updateResetHandler)
}
