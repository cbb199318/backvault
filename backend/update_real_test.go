package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 真实网络冒烟测试：BV_REAL=1 时才跑，用于在 NAS 上验证测速选源
func TestRealUpdateDownload(t *testing.T) {
	if os.Getenv("BV_REAL") != "1" {
		t.Skip("设置 BV_REAL=1 才运行")
	}
	start := time.Now()
	m, err := fetchManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("清单 v%s，用时 %v", m.Version, time.Since(start).Round(time.Millisecond))
	a := m.Assets["linux-amd64"]
	start = time.Now()
	if err := downloadAsset(a, filepath.Join(t.TempDir(), "pkg.tgz")); err != nil {
		t.Fatal(err)
	}
	t.Logf("下载 %d 字节，用时 %v", a.Size, time.Since(start).Round(time.Millisecond))
}
