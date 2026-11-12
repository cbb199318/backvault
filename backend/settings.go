package main

// 设置页后端：全局设置（上传限速）、导出恢复信息、配置备份与恢复。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Settings：全局设置，存放在 config.json 的 settings 字段。
type Settings struct {
	UploadLimitKB int    `json:"upload_limit_kb"`   // 上传限速 KiB/s，0 = 不限速
	LimitDayOnly  bool   `json:"limit_day_only"`    // 只在 LimitFrom–LimitTo 时段内限速
	LimitFrom     string `json:"limit_from"`        // "08:00"
	LimitTo       string `json:"limit_to"`          // "23:00"
	ConfigSavedAt string `json:"config_saved_at"`   // 上次导出配置的时间（展示用）
	RecoverySaved string `json:"recovery_saved_at"` // 上次导出恢复信息的时间（展示用）
}

func normSettings(s Settings) Settings {
	if s.UploadLimitKB < 0 {
		s.UploadLimitKB = 0
	}
	if _, _, ok := schedHM(s.LimitFrom); !ok {
		s.LimitFrom = "08:00"
	}
	if _, _, ok := schedHM(s.LimitTo); !ok {
		s.LimitTo = "23:00"
	}
	return s
}

// inWindow：now 是否落在 [from, to) 时段内；from > to 表示跨午夜（如 22:00–06:00）。
func inWindow(now time.Time, from, to string) bool {
	fh, fm, ok1 := schedHM(from)
	th, tm, ok2 := schedHM(to)
	if !ok1 || !ok2 {
		return true
	}
	cur := now.Hour()*60 + now.Minute()
	f, t := fh*60+fm, th*60+tm
	if f == t {
		return true
	}
	if f < t {
		return cur >= f && cur < t
	}
	return cur >= f || cur < t
}

// uploadLimitNow：本次备份应使用的上传限速（KiB/s），0 表示不限速。在备份开始时判断一次。
func uploadLimitNow(now time.Time) int {
	cfgMu.Lock()
	s := normSettings(cfg.Settings)
	cfgMu.Unlock()
	if s.UploadLimitKB <= 0 {
		return 0
	}
	if s.LimitDayOnly && !inWindow(now, s.LimitFrom, s.LimitTo) {
		return 0
	}
	return s.UploadLimitKB
}

// ---------------------------------------------------------------------------
// rclone.conf 段落读取（导出时只带上仓库实际用到的远端）
// ---------------------------------------------------------------------------

// remoteOfURI："rclone:ol:115/xxx" → "ol"；非 rclone 仓库返回空串。
func remoteOfURI(uri string) string {
	u := strings.TrimPrefix(uri, "rclone:")
	if u == uri && !strings.HasPrefix(uri, "ol:") {
		return ""
	}
	if i := strings.Index(u, ":"); i > 0 {
		return u[:i]
	}
	return ""
}

// readRcloneSections：解析 rclone.conf，返回 段名 → 键值对（保持原顺序）。
func readRcloneSections() map[string][][2]string {
	data, _ := os.ReadFile(filepath.Join(varDir(), "rclone.conf"))
	out := map[string][][2]string{}
	cur := ""
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			cur = strings.TrimSuffix(strings.TrimPrefix(t, "["), "]")
			if _, ok := out[cur]; !ok {
				out[cur] = nil
			}
			continue
		}
		if cur == "" {
			continue
		}
		if i := strings.Index(t, "="); i > 0 {
			out[cur] = append(out[cur], [2]string{strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:])})
		}
	}
	return out
}

// usedRcloneSections：仓库用到的 rclone 远端段落，按名字排序。调用方需持有 cfgMu。
func usedRcloneSectionsLocked() ([]string, map[string][][2]string) {
	all := readRcloneSections()
	seen := map[string]bool{}
	var names []string
	for _, rp := range cfg.Repos {
		n := remoteOfURI(rp.URI)
		if n == "" || seen[n] {
			continue
		}
		if _, ok := all[n]; ok {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	used := map[string][][2]string{}
	for _, n := range names {
		used[n] = all[n]
	}
	return names, used
}

func rcloneConfText(names []string, secs map[string][][2]string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("[" + n + "]\n")
		for _, kv := range secs[n] {
			b.WriteString(kv[0] + " = " + kv[1] + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

func registerSettingsRoutes(mux *http.ServeMux, base string) {
	mux.HandleFunc(base+"/api/settings", settingsHandler)
	mux.HandleFunc(base+"/api/export/recovery", exportRecoveryHandler)
	mux.HandleFunc(base+"/api/export/config", exportConfigHandler)
	mux.HandleFunc(base+"/api/import/config", importConfigHandler)
}

func adminOnly(w http.ResponseWriter, r *http.Request) bool {
	if id := identityFrom(r); id.OK && !id.Admin {
		writeJSON(w, 403, map[string]any{"error": "只有管理员可以执行这个操作"})
		return false
	}
	return true
}

func settingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfgMu.Lock()
		s := normSettings(cfg.Settings)
		cfgMu.Unlock()
		writeJSON(w, 200, s)
	case http.MethodPost, http.MethodPut:
		if !adminOnly(w, r) {
			return
		}
		var in Settings
		if err := readBody(r, &in); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		cfgMu.Lock()
		cur := cfg.Settings
		cur.UploadLimitKB = in.UploadLimitKB
		cur.LimitDayOnly = in.LimitDayOnly
		cur.LimitFrom = in.LimitFrom
		cur.LimitTo = in.LimitTo
		cfg.Settings = normSettings(cur)
		err := saveConfig()
		s := cfg.Settings
		cfgMu.Unlock()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, s)
	default:
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
	}
}

var cloudNames = map[string]string{"tianyi": "天翼云盘", "y115": "115 网盘", "webdav": "WebDAV", "other": "其他"}

func attachment(w http.ResponseWriter, name, ctype string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		strings.Map(func(r rune) rune {
			if r > 127 {
				return '_'
			}
			return r
		}, name), urlEscape(name)))
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// exportRecoveryHandler：导出恢复信息（纯文本）。NAS 损坏时凭这份文件在任意电脑上用 restic 取回数据。
func exportRecoveryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if !adminOnly(w, r) {
		return
	}
	now := time.Now()
	cfgMu.Lock()
	repos := append([]Repo(nil), cfg.Repos...)
	tasks := append([]Task(nil), cfg.Tasks...)
	names, secs := usedRcloneSectionsLocked()
	cfg.Settings.RecoverySaved = now.Format(time.RFC3339)
	_ = saveConfig()
	cfgMu.Unlock()

	var b strings.Builder
	b.WriteString("量子密备 · 恢复信息\n")
	b.WriteString("导出时间：" + now.Format("2006-01-02 15:04") + "    应用版本：" + appVersion + "\n\n")
	b.WriteString("！！这份文件包含所有云端仓库的加密密码和云盘连接凭据。\n")
	b.WriteString("！！请打印或存放在 NAS 以外的安全位置（如密码管理器、加密 U 盘），不要上传到网盘。\n\n")
	b.WriteString("=====================================================================\n")
	b.WriteString("一、仓库与密码\n")
	b.WriteString("=====================================================================\n\n")
	for i, rp := range repos {
		cloud := cloudNames[rp.Cloud]
		if cloud == "" {
			cloud = rp.Cloud
		}
		fmt.Fprintf(&b, "%d. %s（%s）\n", i+1, rp.Name, cloud)
		fmt.Fprintf(&b, "   RESTIC_REPOSITORY = %s\n", rp.URI)
		fmt.Fprintf(&b, "   RESTIC_PASSWORD   = %s\n", rp.Password)
		if rp.DavURL != "" {
			fmt.Fprintf(&b, "   WebDAV 地址       = %s\n", rp.DavURL)
			fmt.Fprintf(&b, "   WebDAV 账号       = %s\n", rp.DavUser)
		}
		var used []string
		for _, t := range tasks {
			if t.RepoID == rp.ID {
				used = append(used, t.Name+"（"+strings.Join(t.Paths, "、")+"）")
			}
		}
		if len(used) > 0 {
			fmt.Fprintf(&b, "   备份任务          = %s\n", strings.Join(used, "；"))
		}
		b.WriteString("\n")
	}
	if len(repos) == 0 {
		b.WriteString("（还没有云端仓库）\n\n")
	}
	if len(names) > 0 {
		b.WriteString("=====================================================================\n")
		b.WriteString("二、rclone 配置（保存为 rclone.conf）\n")
		b.WriteString("=====================================================================\n\n")
		b.WriteString(rcloneConfText(names, secs))
	}
	b.WriteString("=====================================================================\n")
	b.WriteString("三、在别的电脑上取回数据\n")
	b.WriteString("=====================================================================\n\n")
	b.WriteString("1. 安装 restic 和 rclone：https://restic.net  https://rclone.org\n")
	b.WriteString("2. 把第二部分保存为 rclone.conf（没有第二部分的仓库可跳过）\n")
	b.WriteString("3. 在终端执行（把下面两个值换成第一部分里对应仓库的值）：\n\n")
	b.WriteString("   export RCLONE_CONFIG=./rclone.conf\n")
	b.WriteString("   export RESTIC_REPOSITORY='仓库地址'\n")
	b.WriteString("   export RESTIC_PASSWORD='加密密码'\n")
	b.WriteString("   restic snapshots                         # 列出所有快照\n")
	b.WriteString("   restic restore latest --target ./恢复     # 取回最新一次备份\n\n")
	b.WriteString("如果 rclone 远端指向 NAS 上的 OpenList（地址是内网 IP），需要先在新环境里把对应网盘\n")
	b.WriteString("重新挂载到 OpenList，或直接用 rclone 配置该网盘，再把仓库地址里的远端名换成新的。\n")

	attachment(w, "量子密备-恢复信息-"+now.Format("20060102")+".txt", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// configBundle：配置备份文件格式。
type configBundle struct {
	App        string   `json:"app"`
	Kind       string   `json:"kind"`
	Version    string   `json:"version"`
	ExportedAt string   `json:"exported_at"`
	Schema     int      `json:"schema"`
	Repos      []Repo   `json:"repos"`
	Tasks      []Task   `json:"tasks"`
	Settings   Settings `json:"settings"`
	RcloneConf string   `json:"rclone_conf"`
}

func exportConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if !adminOnly(w, r) {
		return
	}
	now := time.Now()
	cfgMu.Lock()
	names, secs := usedRcloneSectionsLocked()
	cfg.Settings.ConfigSavedAt = now.Format(time.RFC3339)
	_ = saveConfig()
	bd := configBundle{
		App: "encbackup", Kind: "config", Version: appVersion, ExportedAt: now.Format(time.RFC3339),
		Schema: cfg.Schema, Repos: append([]Repo(nil), cfg.Repos...), Tasks: append([]Task(nil), cfg.Tasks...),
		Settings: cfg.Settings, RcloneConf: rcloneConfText(names, secs),
	}
	cfgMu.Unlock()
	data, _ := json.MarshalIndent(bd, "", "  ")
	attachment(w, "量子密备-配置备份-"+now.Format("20060102-1504")+".json", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

// importConfigHandler：用配置备份文件替换当前的仓库、任务和设置（还原历史保留）。
// 替换前把当前 config.json 另存一份 config.json.bak-时间戳。
func importConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if !adminOnly(w, r) {
		return
	}
	if reason := busyReason(); reason != "" {
		writeJSON(w, 409, map[string]string{"error": strings.Replace(reason, "再更新", "再导入", 1)})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var bd configBundle
	if err := json.NewDecoder(r.Body).Decode(&bd); err != nil {
		writeJSON(w, 400, map[string]string{"error": "文件格式不对：" + err.Error()})
		return
	}
	if bd.App != "encbackup" || bd.Kind != "config" {
		writeJSON(w, 400, map[string]string{"error": "这不是量子密备导出的配置备份文件"})
		return
	}
	for _, rp := range bd.Repos {
		if rp.ID == "" || rp.URI == "" || rp.Password == "" || rp.Password == "******" {
			writeJSON(w, 400, map[string]string{"error": "配置文件里的仓库「" + rp.Name + "」缺少地址或密码"})
			return
		}
	}

	// rclone 远端先写，失败就不动 config
	secs := map[string][][2]string{}
	cur := ""
	for _, line := range strings.Split(bd.RcloneConf, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			cur = strings.TrimSuffix(strings.TrimPrefix(t, "["), "]")
			secs[cur] = nil
		} else if i := strings.Index(t, "="); i > 0 && cur != "" {
			secs[cur] = append(secs[cur], [2]string{strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:])})
		}
	}
	for name, kvs := range secs {
		if err := writeRcloneSection(name, kvs); err != nil {
			writeJSON(w, 500, map[string]string{"error": "写入 rclone 配置失败：" + err.Error()})
			return
		}
	}

	cfgMu.Lock()
	defer cfgMu.Unlock()
	src := filepath.Join(varDir(), "config.json")
	if old, err := os.ReadFile(src); err == nil {
		_ = os.WriteFile(src+".bak-"+time.Now().Format("20060102-150405"), old, 0o600)
	}
	for i := range bd.Tasks {
		if bd.Tasks[i].LastStatus == "running" {
			bd.Tasks[i].LastStatus = ""
		}
		bd.Tasks[i].Password = ""
	}
	cfg.Repos = bd.Repos
	cfg.Tasks = bd.Tasks
	saved := cfg.Settings
	cfg.Settings = normSettings(bd.Settings)
	cfg.Settings.ConfigSavedAt, cfg.Settings.RecoverySaved = saved.ConfigSavedAt, saved.RecoverySaved
	if err := saveConfig(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "repos": len(cfg.Repos), "tasks": len(cfg.Tasks)})
}
