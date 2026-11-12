// updsign：在线更新的密钥与发布清单工具（只在开发机上运行，不会打进安装包）
//
//	go run ./cmd/updsign keygen  -key ~/.config/encbackup/update_ed25519.key -pubout update_pubkey.go
//	go run ./cmd/updsign release -key ... -version 0.4.5 -base-url https://github.com/o/r/releases/download/v0.4.5 \
//	    -changelog ../CHANGELOG.md -out dist/v0.4.5 dist/v0.4.5/encbackup-0.4.5-linux-amd64.tar.gz ...
//
// 私钥一旦丢失，已装出去的版本将无法再收到在线更新（只能走商店）；一旦泄露，别人可以
// 给你的用户推送任意代码。务必离线备份、不要提交到仓库。
package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

func loadKey(p string) ed25519.PrivateKey {
	b, err := os.ReadFile(expand(p))
	if err != nil {
		die("读取私钥失败：%v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != ed25519.SeedSize {
		die("私钥格式不正确（应为 base64 编码的 32 字节 seed）")
	}
	return ed25519.NewKeyFromSeed(raw)
}

func writePubGo(path string, pub ed25519.PublicKey) {
	src := fmt.Sprintf(`package main

// 在线更新签名公钥（ed25519）。由 go run ./cmd/updsign keygen 生成。
// 更换公钥会让所有已安装版本拒绝新的更新包——除非先发一个同时认新旧两把钥匙的过渡版本。
const updatePubKey = %q
`, base64.StdEncoding.EncodeToString(pub))
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		die("写入公钥失败：%v", err)
	}
}

func keygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	key := fs.String("key", "~/.config/encbackup/update_ed25519.key", "私钥保存位置")
	pubout := fs.String("pubout", "update_pubkey.go", "公钥 Go 源文件")
	force := fs.Bool("force", false, "覆盖已有私钥（危险）")
	_ = fs.Parse(args)
	kp := expand(*key)
	if _, err := os.Stat(kp); err == nil && !*force {
		fmt.Println("私钥已存在，只重新导出公钥：", kp)
		writePubGo(*pubout, loadKey(kp).Public().(ed25519.PublicKey))
		return
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		die("%v", err)
	}
	_ = os.MkdirAll(filepath.Dir(kp), 0o700)
	if err := os.WriteFile(kp, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		die("写入私钥失败：%v", err)
	}
	writePubGo(*pubout, pub)
	fmt.Println("私钥：", kp, "（请离线备份，切勿提交）")
	fmt.Println("公钥：", *pubout)
}

type asset struct {
	Name   string   `json:"name"`
	URLs   []string `json:"urls"`
	SHA256 string   `json:"sha256"`
	Size   int64    `json:"size"`
}

type note struct {
	Version string   `json:"version"`
	Date    string   `json:"date,omitempty"`
	Items   []string `json:"items"`
}

type manifest struct {
	Version         string           `json:"version"`
	MinFrom         string           `json:"min_from,omitempty"`
	MinStoreVersion string           `json:"min_store_version,omitempty"`
	PublishedAt     string           `json:"published_at"`
	Notes           []note           `json:"notes,omitempty"`
	Assets          map[string]asset `json:"assets"`
}

var (
	headRe  = regexp.MustCompile(`^##\s+v?(\d+\.\d+\.\d+\S*)\s*(?:[（(]?\s*(\d{4}-\d{2}-\d{2})\s*[)）]?)?`)
	mdStrip = strings.NewReplacer("**", "", "`", "")
	assetRe = regexp.MustCompile(`-(linux-[a-z0-9]+)\.tar\.gz$`)
)

// parseChangelog 读取 "## x.y.z" 分节、"- " 开头的条目（缩进行并入上一条）
func parseChangelog(p string, limit int) []note {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []note
	var cur *note
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := headRe.FindStringSubmatch(line); m != nil {
			if len(out) >= limit {
				break
			}
			out = append(out, note{Version: m[1], Date: m[2]})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "- "):
			cur.Items = append(cur.Items, mdStrip.Replace(strings.TrimPrefix(t, "- ")))
		case t != "" && len(cur.Items) > 0 && strings.HasPrefix(line, " "):
			prev := cur.Items[len(cur.Items)-1]
			t = mdStrip.Replace(t)
			// 中文直接相连，英文/数字之间补一个空格
			if prev != "" && isASCIIWord(prev[len(prev)-1]) && isASCIIWord(t[0]) {
				prev += " "
			}
			cur.Items[len(cur.Items)-1] = prev + t
		}
	}
	return out
}

func isASCIIWord(c byte) bool {
	return c < 0x80 && (c == '.' || c == ',' || c == ')' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z')
}

func release(args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	key := fs.String("key", "~/.config/encbackup/update_ed25519.key", "私钥")
	ver := fs.String("version", "", "发布版本号")
	minFrom := fs.String("min-from", "", "允许在线更新的最低当前版本")
	minStore := fs.String("min-store", "", "要求的最低商店内置版本")
	baseURL := fs.String("base-url", "", "安装包下载地址前缀（GitHub release download 目录）")
	changelog := fs.String("changelog", "", "CHANGELOG.md 路径")
	outDir := fs.String("out", ".", "latest.json 输出目录")
	_ = fs.Parse(args)
	if *ver == "" || *baseURL == "" || fs.NArg() == 0 {
		die("用法：release -version V -base-url URL [-min-from X] [-min-store Y] [-changelog F] -out DIR 包1.tar.gz ...")
	}
	priv := loadKey(*key)

	m := manifest{
		Version: *ver, MinFrom: *minFrom, MinStoreVersion: *minStore,
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
		Assets:      map[string]asset{},
	}
	if *changelog != "" {
		m.Notes = parseChangelog(*changelog, 15)
		if len(m.Notes) == 0 || m.Notes[0].Version != *ver {
			die("CHANGELOG 第一节不是 %s，请先写更新日志", *ver)
		}
	}
	for _, p := range fs.Args() {
		mm := assetRe.FindStringSubmatch(filepath.Base(p))
		if mm == nil {
			die("无法从文件名识别架构：%s（应形如 xxx-linux-amd64.tar.gz）", p)
		}
		f, err := os.Open(p)
		if err != nil {
			die("%v", err)
		}
		h := sha256.New()
		n, _ := io.Copy(h, f)
		f.Close()
		name := filepath.Base(p)
		m.Assets[mm[1]] = asset{
			Name:   name,
			URLs:   []string{strings.TrimSuffix(*baseURL, "/") + "/" + name},
			SHA256: hex.EncodeToString(h.Sum(nil)),
			Size:   n,
		}
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	sig := ed25519.Sign(priv, data)
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), data, sig) {
		die("自检失败")
	}
	_ = os.MkdirAll(*outDir, 0o755)
	if err := os.WriteFile(filepath.Join(*outDir, "latest.json"), data, 0o644); err != nil {
		die("%v", err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "latest.json.sig"), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		die("%v", err)
	}
	fmt.Printf("已生成 %s/latest.json(.sig)：%s，%d 个架构，%d 条更新日志\n", *outDir, *ver, len(m.Assets), len(m.Notes))
}

func main() {
	if len(os.Args) < 2 {
		die("用法：updsign keygen|release ...")
	}
	switch os.Args[1] {
	case "keygen":
		keygen(os.Args[2:])
	case "release":
		release(os.Args[2:])
	default:
		die("未知命令：%s", os.Args[1])
	}
}
