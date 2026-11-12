package main

// 在线更新的下载：多个来源同时测速，选最快的继续下载。
//
// 背景：国内访问 GitHub 往往不是连不上，而是能连上但只有几 KB/s。
// 以前「直连失败才换镜像」的策略会一直卡在最慢的直连上（实测 4 KB/s，3 MB 要 12 分钟），
// 而镜像能到几百 KB/s。现在改成：
//  1. 所有候选地址同时开始下载，谁先下到 raceBytes 谁赢，其余立刻取消；
//  2. 赢家下载途中如果持续 stallWindow 低于 stallMinRate，判定为卡住，换剩下的来源重新比；
//  3. 每个来源的数据都单独做 sha256 校验，任何一个来源都不会被信任。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	raceBytes    int64 = 512 << 10        // 先下到这么多字节的来源胜出
	stallWindow        = 10 * time.Second // 赢家测速窗口
	stallMinRate int64 = 20 << 10         // 窗口内平均低于这个速度（B/s）判定卡住
	raceTimeout        = 15 * time.Minute // 单轮下载总时限
)

var (
	errLostRace = errors.New("其他来源更快")
	errStalled  = errors.New("下载速度过慢")
)

// downloadAsset 把安装包下载到 dst，并校验大小与 sha256。
func downloadAsset(a updAsset, dst string) error {
	pending := candidates(a.URLs)
	if len(pending) == 0 {
		return errors.New("下载失败：清单里没有下载地址")
	}
	var lastErr error
	for len(pending) > 0 {
		setPhase("downloading", 0, "")
		win, err := raceOnce(pending, a, dst)
		if err == nil {
			log.Printf("[update] 已从 %s 下载完成", shortHost(pending[win]))
			return nil
		}
		lastErr = err
		if win < 0 {
			break // 所有来源都失败了
		}
		log.Printf("[update] %s 中途失败（%v），换其他来源重试", shortHost(pending[win]), err)
		pending = append(append([]string(nil), pending[:win]...), pending[win+1:]...)
	}
	return fmt.Errorf("下载失败：%w", lastErr)
}

// raceOnce 让 urls 同时下载，返回胜出者下标。
// 胜出者下载成功：(idx, nil)；胜出者中途失败：(idx, err)；无人胜出：(-1, err)。
func raceOnce(urls []string, a updAsset, dst string) (int, error) {
	parent, cancelAll := context.WithTimeout(context.Background(), raceTimeout)
	defer cancelAll()

	var winner atomic.Int32
	winner.Store(-1)
	cancels := make([]context.CancelCauseFunc, len(urls))
	errs := make([]error, len(urls))
	tmps := make([]string, len(urls))
	var mu sync.Mutex // 保护 cancels
	var wg sync.WaitGroup

	claim := func(i int) bool {
		if winner.CompareAndSwap(-1, int32(i)) {
			mu.Lock()
			for j, c := range cancels {
				if j != i && c != nil {
					c(errLostRace)
				}
			}
			mu.Unlock()
			return true
		}
		return winner.Load() == int32(i)
	}

	for i, u := range urls {
		ctx, cancel := context.WithCancelCause(parent)
		mu.Lock()
		cancels[i] = cancel
		mu.Unlock()
		tmps[i] = fmt.Sprintf("%s.part%d", dst, i)
		wg.Add(1)
		go func(i int, u string, ctx context.Context, cancel context.CancelCauseFunc) {
			defer wg.Done()
			defer cancel(nil)
			errs[i] = fetchTo(ctx, cancel, u, tmps[i], a, func() bool { return claim(i) }, func() bool { return winner.Load() == int32(i) })
			if errs[i] != nil {
				if c := context.Cause(ctx); c != nil && !errors.Is(c, context.Canceled) {
					errs[i] = c
				}
			}
		}(i, u, ctx, cancel)
	}
	wg.Wait()

	w := int(winner.Load())
	for i, t := range tmps {
		if i != w || errs[i] != nil {
			_ = os.Remove(t)
		}
	}
	if w >= 0 {
		if errs[w] != nil {
			return w, errs[w]
		}
		if err := os.Rename(tmps[w], dst); err != nil {
			_ = os.Remove(tmps[w])
			return w, err
		}
		return w, nil
	}
	var lastErr error
	for i, e := range errs {
		if e != nil && !errors.Is(e, errLostRace) {
			log.Printf("[update] 从 %s 下载失败：%v", shortHost(urls[i]), e)
			lastErr = e
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载来源")
	}
	return -1, lastErr
}

// fetchTo 下载单个来源。claim 在达到胜出条件时调用，返回 false 表示已输给别人；
// isWinner 用来决定是否上报进度、是否做卡顿检测。
func fetchTo(ctx context.Context, cancel context.CancelCauseFunc, url, tmp string, a updAsset, claim, isWinner func() bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "encbackup-updater/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	var got atomic.Int64
	// 卡顿看门狗：只盯胜出者。阻塞在 Read 上也能靠取消上下文打断
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(stallWindow)
		defer t.Stop()
		last := got.Load()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				cur := got.Load()
				if isWinner() && float64(cur-last) < float64(stallMinRate)*stallWindow.Seconds() {
					cancel(errStalled)
					return
				}
				last = cur
			}
		}
	}()

	h := sha256.New()
	buf := make([]byte, 32<<10)
	var n int64
	won := false
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			if _, err := f.Write(buf[:k]); err != nil {
				return err
			}
			h.Write(buf[:k])
			n += int64(k)
			got.Store(n)
			if n > maxPackageBytes {
				return errors.New("安装包超过大小上限")
			}
			if !won && n >= raceBytes {
				if !claim() {
					return errLostRace
				}
				won = true
			}
			if won && a.Size > 0 {
				setPhase("downloading", float64(n)/float64(a.Size), "")
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if !won && !claim() { // 小于 raceBytes 的包：先下完的胜出
		return errLostRace
	}
	if a.Size > 0 && n != a.Size {
		return fmt.Errorf("大小不符（%d / %d）", n, a.Size)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(sum, a.SHA256) {
		return errors.New("sha256 校验失败")
	}
	return f.Sync()
}

// fetchManifest 同时向所有来源请求更新清单，返回最先到达且签名校验通过的那份。
func fetchManifest(ctx context.Context) (*updManifest, error) {
	urls := manifestURLs()
	if len(urls) == 0 {
		return nil, errors.New("没有可用的更新源")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	type res struct {
		m   *updManifest
		err error
	}
	ch := make(chan res, len(urls))
	for _, u := range urls {
		go func(u string) {
			m, err := fetchManifestFrom(ctx, u)
			if err != nil {
				err = fmt.Errorf("%s：%w", shortHost(u), err)
			}
			ch <- res{m, err}
		}(u)
	}
	// 镜像可能缓存了旧清单：拿到第一份后再等一小会儿，取版本号最高的
	var best *updManifest
	var lastErr error
	var grace <-chan time.Time
	for left := len(urls); left > 0; {
		select {
		case r := <-ch:
			left--
			if r.err != nil {
				lastErr = r.err
				continue
			}
			if best == nil || cmpVer(r.m.Version, best.Version) > 0 {
				best = r.m
			}
			if grace == nil {
				grace = time.After(manifestGrace)
			}
		case <-grace:
			return best, nil
		}
	}
	if best != nil {
		return best, nil
	}
	return nil, lastErr
}

var manifestGrace = 2 * time.Second

func fetchManifestFrom(ctx context.Context, u string) (*updManifest, error) {
	body, err := httpGet(ctx, u, 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := httpGet(ctx, u+".sig", 4<<10)
	if err != nil {
		return nil, err
	}
	if err := verifySig(body, sig); err != nil {
		return nil, err
	}
	var m updManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("清单格式错误：%w", err)
	}
	if !safeVerRe.MatchString(m.Version) {
		return nil, fmt.Errorf("清单版本号非法：%q", m.Version)
	}
	return &m, nil
}
