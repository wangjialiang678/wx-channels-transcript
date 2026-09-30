// Package session 管理元宝（yuanbao.tencent.com）的登录态：
// 获取、验证、缓存、续期。
//
// 设计取舍：
//   - 优先用缓存。拿一次 cookie 存起来，之后不再碰浏览器、不再弹钥匙串。
//   - 服务端会续期。getuserinfo 响应里可能带回新的会话 cookie，
//     必须写回缓存，否则放旧的用不了多久。
//   - 权限比凭证重要。这里只存「这个账号的会话」，不存用户的任何其他数据。
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wangjialiang678/wx-channels-transcript/internal/cookies"
)

const (
	// YuanbaoBase 是元宝站点根。
	YuanbaoBase = "https://yuanbao.tencent.com"
	// UserInfoURL 用来验证登录态是否有效。
	UserInfoURL = YuanbaoBase + "/api/getuserinfo"
	// SessionHost 是登录态 cookie 的作用域基准域名。
	SessionHost = "yuanbao.tencent.com"

	verifyTimeout = 15 * time.Second
)

// KeychainNotice 是真正触发钥匙串之前必须先说清楚的话。
const KeychainNotice = `
────────────────────────────────────────────────────────────
接下来 macOS 可能弹出一个「钥匙串」授权窗口，请先看这里：

  · 它不是你正在用的程序弹的，是 macOS 的安全机制
  · 要输入的是：你的 Mac 登录密码（开机密码）
  · 用途是：解密 Chrome 里保存的元宝登录态
  · 我们只读取 ` + SessionHost + ` 这一个域名下的 cookie，
    不会读取其他任何网站

  建议点「始终允许」——下次就不再询问。

  不想授权？用 --cookie "<粘贴的cookie>" 可完全跳过这一步。
────────────────────────────────────────────────────────────
`

var maxAgeDeleteRe = regexp.MustCompile(`(?i)^\s*max-age\s*=\s*(0|-\d+)\s*$`)

// CacheFile 是落盘的登录态缓存。
// 字段名刻意与 Node 版保持一致，两版共用同一个文件。
type CacheFile struct {
	SavedAt string            `json:"savedAt"`
	Host    string            `json:"host"`
	Note    string            `json:"note,omitempty"`
	Jar     map[string]string `json:"jar"`
}

// Session 持有一次会话的登录态。
type Session struct {
	path   string
	jar    cookies.Jar
	origin string
}

// DefaultPath 返回登录态缓存的默认路径（可用 SPH_SESSION 覆盖）。
func DefaultPath() string {
	if p := os.Getenv("SPH_SESSION"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".sph-session.json"
	}
	return filepath.Join(home, ".config/sph-transcript/session.json")
}

// New 创建会话对象。path 为空时用 DefaultPath()。
func New(path string) *Session {
	if path == "" {
		path = DefaultPath()
	}
	return &Session{path: path, origin: "none"}
}

// Path 返回缓存文件路径。
func (s *Session) Path() string { return s.path }

// Origin 返回本次登录态的来源：arg / cache / browser:<名称> / none。
func (s *Session) Origin() string { return s.origin }

// CookieHeader 返回可直接放进 Cookie 头的字符串，未取得登录态时返回空串。
func (s *Session) CookieHeader() string {
	if s.jar == nil {
		return ""
	}
	return s.jar.Header()
}

// VerifyResult 是一次登录态校验的结果。
type VerifyResult struct {
	OK      bool
	Status  int
	Renewed cookies.Jar // 服务端在本次响应里下发的续期 cookie
}

// Verify 校验一个 cookie 头是否可用，并顺带收取服务端的续期。
func (s *Session) Verify(ctx context.Context, header string) (VerifyResult, error) {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UserInfoURL, nil)
	if err != nil {
		return VerifyResult{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Source", "web")
	req.Header.Set("Origin", YuanbaoBase)
	req.Header.Set("Referer", YuanbaoBase+"/")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", header)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("访问元宝失败：%w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	// 只取续期，不理会删除：服务端会先删 host-only 再写域级，
	// 同一个响应里会有两条 Set-Cookie，删的那条是过程性的。
	renewed := make(cookies.Jar)
	for _, sc := range resp.Header.Values("Set-Cookie") {
		parts := strings.Split(sc, ";")
		pair := parts[0]
		i := strings.IndexByte(pair, '=')
		if i <= 0 {
			continue
		}
		name := strings.TrimSpace(pair[:i])
		value := strings.TrimSpace(pair[i+1:])
		deleting := false
		for _, a := range parts[1:] {
			if maxAgeDeleteRe.MatchString(a) {
				deleting = true
				break
			}
		}
		if value == "" || deleting {
			continue
		}
		if hasControlChars(value) {
			continue
		}
		renewed[name] = value
	}

	return VerifyResult{OK: resp.StatusCode == http.StatusOK, Status: resp.StatusCode, Renewed: renewed}, nil
}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"

func hasControlChars(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 0x00 && c <= 0x08) || (c >= 0x0a && c <= 0x1f) || c == 0x7f {
			return true
		}
	}
	return false
}

// LoadCache 读取缓存文件；不存在或格式不对时返回 nil。
func (s *Session) LoadCache() *CacheFile {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var d CacheFile
	if err := json.Unmarshal(b, &d); err != nil {
		return nil
	}
	if len(d.Jar) == 0 {
		return nil
	}
	return &d
}

// SaveCache 把 jar 写回缓存文件（0600 权限）。
func (s *Session) SaveCache(jar cookies.Jar) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(CacheFile{
		SavedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Host:    SessionHost,
		Note:    "元宝会话 cookie 缓存。由 sph-transcript 自动维护，可直接删除以强制重新授权。",
		Jar:     jar,
	}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(s.path, b, 0o600)
}

// ClearCache 删除缓存文件（不存在时什么都不做）。
func (s *Session) ClearCache() error {
	err := os.Remove(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// AcquireOptions 控制登录态的获取策略。
type AcquireOptions struct {
	Cookie       string                             // --cookie 传入的显式登录态
	Browser      string                             // --browser 指定的浏览器
	AllowBrowser bool                               // 是否允许读浏览器
	OnNotice     func(string)                       // 读浏览器前先打印提示
	Verify       func(string) (VerifyResult, error) // 校验函数（测试时可替换）
}

// AcquireResult 描述这次登录态是从哪来的。
type AcquireResult struct {
	Origin  string // arg / cache / browser:<名称>
	Browser string
	Count   int
	Renewed int
}

// Acquire 按优先级取得一个可用的登录态：
//
//  1. --cookie 显式传入
//  2. 本地缓存
//  3. 读浏览器（会弹钥匙串，调用方应先打印 KeychainNotice）
//  4. 都失败 → 返回带指引的错误
func (s *Session) Acquire(ctx context.Context, opts AcquireOptions) (AcquireResult, error) {
	verify := opts.Verify
	if verify == nil {
		verify = func(h string) (VerifyResult, error) { return s.Verify(ctx, h) }
	}

	// 1) 显式传入
	if opts.Cookie != "" {
		v, err := verify(opts.Cookie)
		if err != nil {
			return AcquireResult{}, err
		}
		if !v.OK {
			return AcquireResult{}, fmt.Errorf("--cookie 传入的登录态无效（getuserinfo 返回 HTTP %d）。"+
				"请重新从浏览器复制，或去掉 --cookie 让它自动读取", v.Status)
		}
		s.jar = cookies.ParseHeader(opts.Cookie)
		if v.Renewed != nil {
			s.jar = s.jar.Merge(v.Renewed)
		}
		s.origin = "arg"
		_ = s.SaveCache(s.jar)
		return AcquireResult{Origin: s.origin}, nil
	}

	// 2) 缓存
	if cached := s.LoadCache(); cached != nil {
		jar := cookies.Jar(cached.Jar)
		v, err := verify(jar.Header())
		if err != nil {
			return AcquireResult{}, err
		}
		if v.OK {
			s.jar = jar.Merge(v.Renewed)
			s.origin = "cache"
			if len(v.Renewed) > 0 {
				_ = s.SaveCache(s.jar)
			}
			return AcquireResult{Origin: s.origin, Renewed: len(v.Renewed)}, nil
		}
		// 缓存失效：清掉，继续往下走
		_ = s.ClearCache()
	}

	if !opts.AllowBrowser {
		return AcquireResult{}, fmt.Errorf("登录态已失效，且当前不允许读取浏览器。请用 --cookie 重新提供")
	}

	// 3) 读浏览器
	if opts.OnNotice != nil {
		opts.OnNotice(KeychainNotice)
	}
	jar, used, err := cookies.ReadBrowserCookies(SessionHost, opts.Browser)
	if err != nil {
		return AcquireResult{}, err
	}
	if len(jar) == 0 {
		return AcquireResult{}, fmt.Errorf("在 %s 里没有找到 %s 的 cookie。\n"+
			"  请先用 %s 打开 %s 并扫码登录，然后重试。\n"+
			"  或者用 --cookie 手工提供登录态", used, SessionHost, used, YuanbaoBase)
	}
	v, err := verify(jar.Header())
	if err != nil {
		return AcquireResult{}, err
	}
	if !v.OK {
		return AcquireResult{}, fmt.Errorf("读取到的 cookie 无法通过元宝校验（HTTP %d）。\n"+
			"  通常是登录态已过期：请用 %s 重新打开 %s 扫码登录。\n"+
			"  （若浏览器里确实已登录，可尝试 --browser 指定其它浏览器）", v.Status, used, YuanbaoBase)
	}
	s.jar = jar.Merge(v.Renewed)
	s.origin = "browser:" + used
	_ = s.SaveCache(s.jar)
	return AcquireResult{Origin: s.origin, Count: len(jar), Browser: used}, nil
}
