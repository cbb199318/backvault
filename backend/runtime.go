package main

// 以应用用户（run-as package，非 root）运行时的自检与提示。
//
// 0.5.0 起应用不再以 root 运行。从旧版本（root）升级上来时，数据目录里的文件可能仍归 root 所有、
// 权限 600，应用用户读不到。这里在启动时检查一次：读不到就记下原因，界面顶部给出修复办法，
// 并且拒绝写配置，避免用一份空配置覆盖掉旧数据。

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"sync"
)

// permHint：读写用户目录被拒时给用户看的话
const permHint = "没有访问权限：请到 应用中心 → 量子密备 → 设置 → 授权目录 里授权这个目录（还原到原位置需要读写权限）"

var (
	dataErrMu sync.RWMutex
	dataErr   string
)

func dataError() string {
	dataErrMu.RLock()
	defer dataErrMu.RUnlock()
	return dataErr
}

func setDataError(s string) {
	dataErrMu.Lock()
	dataErr = s
	dataErrMu.Unlock()
	if s != "" {
		log.Printf("[runtime] %s", s)
	}
}

func runUserName() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return fmt.Sprint(os.Getuid())
}

// checkDataDir 检查数据目录里的关键文件是否可读写。返回 nil 表示正常。
func checkDataDir() error {
	dir := varDir()
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("数据目录 %s 不可写：%w", dir, err)
	}
	_ = os.Remove(probe)
	for _, name := range []string{"config.json", "rclone.conf"} {
		p := filepath.Join(dir, name)
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("读不到 %s：%w", p, err)
		}
		f.Close()
	}
	return nil
}

// dataFixHint 升级后数据目录归 root 所有时，让管理员执行的修复命令
func dataFixHint() string {
	return fmt.Sprintf("请用管理员账号 SSH 登录 NAS 执行：sudo chown -R %s %s ，然后在应用中心重启量子密备。",
		runUserName(), varDir())
}

func startupDataCheck() {
	if err := checkDataDir(); err != nil {
		setDataError("升级后无法访问旧数据（" + err.Error() + "）。" + dataFixHint())
	}
}
