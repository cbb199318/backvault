package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 多用户隔离：admin(1000) / 普通用户 b(1001) / 普通用户 c(1002)
func setupOwnerTest(t *testing.T) *http.ServeMux {
	t.Setenv("SOCKET_PATH", "/tmp/encbackup-test.sock") // 开启网关身份模式
	t.Setenv("TRIM_PKGVAR", t.TempDir())
	cfgMu.Lock()
	cfg = &Config{
		Repos: []Repo{
			{ID: "r-legacy", Name: "旧仓库", URI: "rclone:ol:天翼云盘/旧", Password: "pw0"},
			{ID: "r-admin", Name: "admin 仓库", URI: "rclone:ol:天翼云盘/a", Password: "pw1", Owner: 1000},
			{ID: "r-b", Name: "b 仓库", URI: "rclone:wd-b:b", Password: "pw2", Cloud: "webdav", Remote: "wd-b", Owner: 1001},
		},
		Tasks: []Task{
			{ID: "t-legacy", Name: "旧任务", RepoID: "r-legacy", Paths: []string{"/vol1/1000/x"}},
			{ID: "t-admin", Name: "admin 任务", RepoID: "r-admin", Paths: []string{"/vol1/1000/a"}, Owner: 1000},
			{ID: "t-b", Name: "b 任务", RepoID: "r-b", Paths: []string{"/vol1/1001/b"}, Owner: 1001},
		},
		Restores: []RestoreJob{
			{ID: "rst-a", TaskID: "t-admin", Status: "success"},
			{ID: "rst-b", TaskID: "t-b", Status: "success"},
		},
	}
	cfgMu.Unlock()
	mux := http.NewServeMux()
	registerRoutes(mux, "")
	return mux
}

func call(mux *http.ServeMux, uid string, admin bool, method, path, body string) (int, map[string]any) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if uid != "" {
		req.Header.Set("X-Trim-Userid", uid)
		if admin {
			req.Header.Set("X-Trim-Isadmin", "true")
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func ids(list any) []string {
	var out []string
	for _, x := range list.([]any) {
		out = append(out, x.(map[string]any)["id"].(string))
	}
	return out
}

func TestOwnerConfigVisibility(t *testing.T) {
	mux := setupOwnerTest(t)
	_, b := call(mux, "1001", false, "GET", "/api/config", "")
	if got := ids(b["tasks"]); len(got) != 1 || got[0] != "t-b" {
		t.Fatalf("b 只应看到自己的任务，实际 %v", got)
	}
	if got := ids(b["repos"]); len(got) != 1 || got[0] != "r-b" {
		t.Fatalf("b 只应看到自己的仓库，实际 %v", got)
	}
	if got := ids(b["restores"]); len(got) != 1 || got[0] != "rst-b" {
		t.Fatalf("b 只应看到自己的还原记录，实际 %v", got)
	}
	_, a := call(mux, "1000", true, "GET", "/api/config", "")
	if n := len(a["tasks"].([]any)); n != 3 {
		t.Fatalf("管理员应看到全部 3 个任务，实际 %d", n)
	}
	_, c := call(mux, "1002", false, "GET", "/api/config", "")
	if n := len(c["tasks"].([]any)); n != 0 {
		t.Fatalf("c 不应看到任何任务，实际 %d", n)
	}
}

func TestOwnerGuardsOtherUsersResources(t *testing.T) {
	mux := setupOwnerTest(t)
	cases := []struct{ method, path, body string }{
		{"GET", "/api/repos/r-admin/password", ""},
		{"GET", "/api/repos/r-legacy/password", ""},
		{"POST", "/api/repos/r-admin/delete", ""},
		{"POST", "/api/repos/r-admin/verify", ""},
		{"GET", "/api/repos/r-admin/snapshots", ""},
		{"POST", "/api/tasks/t-admin/run", ""},
		{"POST", "/api/tasks/t-admin/delete", ""},
		{"GET", "/api/tasks/t-admin/snapshots", ""},
		{"POST", "/api/tasks/t-admin/restore", `{"snapshot":"x","includes":["/vol1/1000/a/f"],"dest":"custom","target":"/vol1/1001/b","conflict":"rename"}`},
		{"POST", "/api/repos", `{"id":"r-admin","name":"抢","uri":"rclone:wd-b:x","cloud":"webdav","dav_url":"http://x","dav_user":"u"}`},
		{"POST", "/api/tasks", `{"id":"t-admin","name":"抢","paths":["/vol1/1001/b"],"repo_id":"r-b"}`},
		{"POST", "/api/tasks", `{"name":"借用","paths":["/vol1/1001/b"],"repo_id":"r-admin"}`},
		{"POST", "/api/restores/rst-a/stop", ""},
	}
	for _, c := range cases {
		if code, _ := call(mux, "1001", false, c.method, c.path, c.body); code != 404 {
			t.Errorf("b %s %s 应返回 404，实际 %d", c.method, c.path, code)
		}
	}
	if code, out := call(mux, "1001", false, "GET", "/api/repos/r-b/password", ""); code != 200 || out["password"] != "pw2" {
		t.Errorf("b 应能看自己仓库的密码：%d %v", code, out)
	}
	if code, out := call(mux, "1000", true, "GET", "/api/repos/r-b/password", ""); code != 200 || out["password"] != "pw2" {
		t.Errorf("管理员应能看全部仓库：%d %v", code, out)
	}
}

func TestOwnerCloudRestrictions(t *testing.T) {
	mux := setupOwnerTest(t)
	if code, _ := call(mux, "1001", false, "POST", "/api/browse-cloud", `{"remote":"ol:天翼云盘"}`); code != 403 {
		t.Errorf("b 不应能浏览管理员的天翼云盘，实际 %d", code)
	}
	if code, _ := call(mux, "1001", false, "POST", "/api/browse-cloud", `{"cloud":"天翼云盘"}`); code != 403 {
		t.Errorf("b 不应能用旧参数浏览天翼云盘，实际 %d", code)
	}
	if code, _ := call(mux, "1002", false, "POST", "/api/mkdir-cloud", `{"remote":"wd-b","path":"x"}`); code != 403 {
		t.Errorf("c 不应能在 b 的 WebDAV 上建目录，实际 %d", code)
	}
	if code, out := call(mux, "1001", false, "POST", "/api/tasks", `{"name":"x","paths":["/vol1/1001/b"],"dest_cloud":"tianyi","dest_subdir":"b"}`); code != 400 || !strings.Contains(out["error"].(string), "普通用户") {
		t.Errorf("b 不应能把任务建在管理员的天翼云盘上：%d %v", code, out)
	}
	if code, _ := call(mux, "1001", false, "POST", "/api/repos", `{"name":"x","cloud":"tianyi","uri":"rclone:ol:天翼云盘/x"}`); code != 403 {
		t.Errorf("b 不应能新建天翼云盘仓库，实际 %d", code)
	}
}

func TestOwnerAssignedByServer(t *testing.T) {
	mux := setupOwnerTest(t)
	// b 伪造 owner=1000 新建 WebDAV 连接仓库（URI 以冒号结尾：纯连接，不触发 restic init）
	code, out := call(mux, "1001", false, "POST", "/api/repos", `{"name":"新","cloud":"other","uri":"rclone:wd-new:","owner":1000}`)
	_ = out
	if code != 403 { // 普通用户只能建 WebDAV；cloud=other 被拒
		t.Fatalf("普通用户建非 WebDAV 仓库应被拒：%d", code)
	}
	cfgMu.Lock()
	for _, r := range cfg.Repos {
		if r.Name == "新" {
			t.Fatal("被拒的仓库不应写入配置")
		}
	}
	cfgMu.Unlock()
	// 管理员编辑 b 的仓库，归属不变
	call(mux, "1000", true, "POST", "/api/repos", `{"id":"r-b","name":"改名","uri":"rclone:wd-b:b","cloud":"other","owner":1000}`)
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if rp := findRepo("r-b"); rp == nil || rp.Owner != 1001 || rp.Name != "改名" {
		t.Fatalf("编辑后应保留原归属：%+v", rp)
	}
}

func TestOwnerProgressHidesOthers(t *testing.T) {
	mux := setupOwnerTest(t)
	stateMu.Lock()
	running, runCur = "t-admin", []string{"/vol1/1000/a/秘密.txt"}
	stateMu.Unlock()
	defer func() { stateMu.Lock(); running, runCur = "", nil; stateMu.Unlock() }()
	_, p := call(mux, "1001", false, "GET", "/api/progress", "")
	if p["task_id"] != "" || p["current_files"] != nil || p["busy"] != true {
		t.Fatalf("b 不应看到 admin 任务的进度细节：%v", p)
	}
	_, a := call(mux, "1000", true, "GET", "/api/progress", "")
	if a["task_id"] != "t-admin" {
		t.Fatalf("管理员应看到进度：%v", a)
	}
}
