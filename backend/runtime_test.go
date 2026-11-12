package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 模拟 restic 还原出 d--------- 目录：补权限后能搬移、能删除
func TestEnsureOwnerAccess(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".encbackup-rst-x")
	deep := filepath.Join(root, "vol1", "1000", "资料")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(deep, "a.jpg")
	if err := os.WriteFile(f, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{deep, filepath.Dir(deep), filepath.Dir(filepath.Dir(deep)), root} {
		_ = os.Chmod(d, 0o000)
	}
	ensureOwnerAccess(root)
	dst := filepath.Join(filepath.Dir(root), "a.jpg")
	if err := os.Rename(f, dst); err != nil {
		t.Fatalf("补权限后应能搬移：%v", err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "x" {
		t.Fatalf("文件应可读：%v", err)
	}
	removeStaging(root)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("临时目录应被删除")
	}
}

func TestSweepStaleStaging(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, ".encbackup-rst-old", "sub")
	_ = os.MkdirAll(old, 0o755)
	_ = os.Chmod(old, 0o000)
	_ = os.Chmod(filepath.Dir(old), 0o000)
	keep := filepath.Join(dir, "用户文件")
	_ = os.MkdirAll(keep, 0o755)
	sweepStaleStaging(dir)
	if _, err := os.Stat(filepath.Dir(old)); !os.IsNotExist(err) {
		t.Fatal("残留临时目录应被清理")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("不能误删用户文件")
	}
}
