package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// 以 chunk 为单位、每块间隔 gap 地输出 data；stallAfter>0 时输出这么多字节后挂起
func throttled(data []byte, chunk int, gap time.Duration, stallAfter int, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		fl := w.(http.Flusher)
		for off := 0; off < len(data); off += chunk {
			if stallAfter > 0 && off >= stallAfter {
				<-r.Context().Done()
				return
			}
			end := min(off+chunk, len(data))
			if _, err := w.Write(data[off:end]); err != nil {
				return
			}
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(gap):
			}
		}
	}))
}

func testAsset(data []byte, urls ...string) updAsset {
	sum := sha256.Sum256(data)
	return updAsset{URLs: urls, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
}

func TestDownloadPicksFastest(t *testing.T) {
	data := make([]byte, 2<<20)
	_, _ = rand.Read(data)
	slow := throttled(data, 4<<10, 100*time.Millisecond, 0, nil) // ~40 KB/s
	defer slow.Close()
	fast := throttled(data, 64<<10, 5*time.Millisecond, 0, nil)
	defer fast.Close()

	dst := filepath.Join(t.TempDir(), "pkg.tgz")
	start := time.Now()
	if err := downloadAsset(testAsset(data, slow.URL, fast.URL), dst); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("应该走快的来源，实际用了 %v", d)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, data) {
		t.Fatal("内容不一致")
	}
	if m, _ := filepath.Glob(dst + ".part*"); len(m) != 0 {
		t.Fatalf("临时文件没清理：%v", m)
	}
}

func TestDownloadSwitchesWhenWinnerStalls(t *testing.T) {
	oldW, oldR := stallWindow, stallMinRate
	stallWindow, stallMinRate = 400*time.Millisecond, 50<<10
	defer func() { stallWindow, stallMinRate = oldW, oldR }()

	data := make([]byte, 1536<<10)
	_, _ = rand.Read(data)
	// 一开始最快，下到 768 KB 后卡住
	flaky := throttled(data, 128<<10, 2*time.Millisecond, 768<<10, nil)
	defer flaky.Close()
	var steadyHits atomic.Int32
	steady := throttled(data, 32<<10, 20*time.Millisecond, 0, &steadyHits)
	defer steady.Close()

	dst := filepath.Join(t.TempDir(), "pkg.tgz")
	if err := downloadAsset(testAsset(data, flaky.URL, steady.URL), dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, data) {
		t.Fatal("内容不一致")
	}
	if steadyHits.Load() < 2 {
		t.Fatalf("卡住后应重新向稳定来源发起下载，实际请求 %d 次", steadyHits.Load())
	}
}

func TestDownloadRejectsBadHash(t *testing.T) {
	data := make([]byte, 600<<10)
	_, _ = rand.Read(data)
	srv := throttled(data, 64<<10, time.Millisecond, 0, nil)
	defer srv.Close()
	a := testAsset(data, srv.URL)
	a.SHA256 = hex.EncodeToString(make([]byte, 32))
	if err := downloadAsset(a, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("sha256 不符时应失败")
	}
}

func TestFetchManifestAllFail(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	t.Setenv("UPDATE_MANIFEST_URL", srv.URL+"/latest.json")
	if _, err := fetchManifest(context.Background()); err == nil {
		t.Fatal("全部来源失败时应返回错误")
	}
}
