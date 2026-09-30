// Package cookies 读取 Chromium 系浏览器保存的 cookie（macOS）。
//
// 为什么需要它：视频号解析必须带元宝（yuanbao.tencent.com）的登录态，
// 而这条 cookie 通常已经存在于用户日常使用的浏览器里——直接借用，
// 用户就不必去开 DevTools 手工复制。
//
// macOS 上 Chrome 系浏览器的 cookie 用 AES-128-CBC 加密，主密钥存在
// 系统钥匙串的 "Chrome Safe Storage" 条目里。因此第一次读取会触发
// 一次系统授权弹窗——这是 macOS 的安全机制，不是本程序私自索取。
//
// 三条必须遵守的实现纪律（都踩过）：
//  1. 不要按名字挑 cookie。会话 cookie 未必叫 hy_token，改一次名就静默失效。
//  2. 不要按域名前缀过滤。hy_token / hy_user 实测挂在父域 .tencent.com 下，
//     必须做标准的作用域匹配（host-only 精确匹配 / 域级后缀匹配）。
//  3. 不要对 cookie 值做 UTF-8 校验或转换。有些值是二进制，碰了就坏。
package cookies

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动，无需 cgo
)

const (
	safeStorageService = "Chrome Safe Storage"
	safeStorageAccount = "Chrome"

	// Chrome 用的是固定 salt + 1003 轮，不是随机 salt；IV 固定为 16 个空格。
	pbkdf2Salt     = "saltysalt"
	pbkdf2Iters    = 1003
	pbkdf2KeyBytes = 16
)

// Browser 是一个探测到的 Chromium 系浏览器。
type Browser struct {
	Name string // 短名，用于 --browser 参数
	Dir  string // macOS 下的用户数据目录
}

// 各浏览器在 macOS 下的用户数据目录（相对 ~/Library/Application Support）。
var chromiumBrowsers = []struct{ name, rel string }{
	{"chrome", "Google/Chrome"},
	{"chromium", "Chromium"},
	{"edge", "Microsoft Edge"},
	{"brave", "BraveSoftware/Brave-Browser"},
	{"arc", "Arc"},
	{"vivaldi", "Vivaldi"},
	{"opera", "com.operasoftware.Opera"},
}

// HostMatches 做标准的 cookie 作用域匹配：
// host_key 以点开头表示域级 cookie，对子域同样生效。
func HostMatches(hostKey, requestHost string) bool {
	if strings.HasPrefix(hostKey, ".") {
		d := hostKey[1:]
		return requestHost == d || strings.HasSuffix(requestHost, "."+d)
	}
	return requestHost == hostKey
}

// DetectBrowsers 列出本机实际存在的 Chromium 系浏览器（按常见优先顺序）。
func DetectBrowsers() []Browser {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	base := filepath.Join(home, "Library/Application Support")
	var found []Browser
	for _, b := range chromiumBrowsers {
		dir := filepath.Join(base, b.rel)
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			found = append(found, Browser{Name: b.name, Dir: dir})
		}
	}
	return found
}

// findCookieDB 在浏览器目录下找一个可用的 Cookies 库（新版在 Network/ 子目录）。
func findCookieDB(browserDir string) string {
	candidates := []string{
		filepath.Join(browserDir, "Default", "Network", "Cookies"),
		filepath.Join(browserDir, "Default", "Cookies"),
		filepath.Join(browserDir, "Profile 1", "Network", "Cookies"),
		filepath.Join(browserDir, "Profile 1", "Cookies"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// ChromeKey 从钥匙串取 Chrome 的解密主密钥。
// 第一次调用会弹系统授权窗口——调用方应在此之前向用户解释清楚。
func ChromeKey() (key, iv []byte, err error) {
	out, err := exec.Command("security",
		"find-generic-password", "-w",
		"-s", safeStorageService, "-a", safeStorageAccount,
	).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("从钥匙串读取 %q 失败：%w\n"+
			"  常见原因：点了「拒绝」，或本机不是 macOS。\n"+
			"  替代方案：用 --cookie \"<粘贴的cookie>\" 完全跳过这一步", safeStorageService, err)
	}
	password := strings.TrimSpace(string(out))
	key, err = pbkdf2.Key(sha1.New, password, []byte(pbkdf2Salt), pbkdf2Iters, pbkdf2KeyBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("派生密钥失败：%w", err)
	}
	return key, bytesRepeat(0x20, aes.BlockSize), nil
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// decryptValue 解密一条 encrypted_value。
// 失败时返回空串（调用方会跳过这条 cookie），不中断整体流程。
func decryptValue(enc, key, iv []byte) string {
	if len(enc) < 3 {
		return ""
	}
	prefix := string(enc[:3])
	// v20 是 App-Bound 加密（密钥绑在 app 上），暂不支持。
	if prefix != "v10" && prefix != "v11" {
		return ""
	}
	ct := enc[3:]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	plain, ok := pkcs7Unpad(plain)
	if !ok {
		return ""
	}
	// 新版 Chrome 在明文前塞 32 字节 domain hash，按可打印性判断是否剥掉。
	if len(plain) > 32 && !isPrintableASCII(plain[:32]) {
		plain = plain[32:]
	}
	return stripControlChars(plain)
}

func pkcs7Unpad(b []byte) ([]byte, bool) {
	if len(b) == 0 {
		return nil, false
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, false
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, false
		}
	}
	return b[:len(b)-n], true
}

func isPrintableASCII(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// stripControlChars 去掉会让 HTTP 头发不出去的控制字符。
// 注意保留 \t（0x09），去掉 \x00-\x08、\x0a-\x1f、\x7f。
func stripControlChars(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		if (c >= 0x00 && c <= 0x08) || (c >= 0x0a && c <= 0x1f) || c == 0x7f {
			continue
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// ReadBrowserCookies 读取指定浏览器里、会对 requestHost 生效的全部 cookie。
// browserName 为空时用探测到的第一个浏览器。
func (b Browser) ReadBrowserCookies(requestHost string) (Jar, error) {
	db := findCookieDB(b.Dir)
	if db == "" {
		return nil, fmt.Errorf("%s 下没有找到 Cookies 数据库", b.Name)
	}
	key, iv, err := ChromeKey()
	if err != nil {
		return nil, err
	}

	// 浏览器运行时会锁住数据库，复制一份再读。
	// ⚠️ 这份副本里装的是用户的全部相关 cookie（含登录态），属于敏感数据，
	// 因此无论是正常结束还是中途抛错，都必须删掉——用 defer 保证。
	tmpDir, err := os.MkdirTemp("", "sph-cookies-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmp := filepath.Join(tmpDir, "Cookies")
	if err := copyFile(db, tmp); err != nil {
		return nil, fmt.Errorf("复制 cookie 库失败：%w", err)
	}

	jar, err := readCookiesTable(tmp, requestHost, key, iv)
	if err != nil {
		return nil, err
	}
	return jar, nil
}

func readCookiesTable(path, requestHost string, key, iv []byte) (Jar, error) {
	dsn := "file:" + path + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 cookie 库失败：%w", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT host_key, name, value, encrypted_value FROM cookies")
	if err != nil {
		return nil, fmt.Errorf("查询 cookies 表失败：%w", err)
	}
	defer rows.Close()

	jar := make(Jar)
	for rows.Next() {
		var hostKey, name string
		var plain sql.NullString
		var enc []byte
		if err := rows.Scan(&hostKey, &name, &plain, &enc); err != nil {
			return nil, fmt.Errorf("读取 cookie 行失败：%w", err)
		}
		if !HostMatches(hostKey, requestHost) {
			continue
		}
		v := plain.String
		if v == "" && len(enc) > 0 {
			v = decryptValue(enc, key, iv)
		}
		if v != "" {
			jar[name] = v
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 cookie 行失败：%w", err)
	}
	return jar, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ReadBrowserCookies 是一个便利入口：不关心是哪个浏览器时用它。
func ReadBrowserCookies(requestHost, browserName string) (Jar, string, error) {
	// ⚠️ 自动读取浏览器登录态**目前只实现了 macOS**。
	// 各平台的 cookie 加密方式完全不同，不是改个路径就能解决的：
	//   macOS   → 钥匙串（Chrome Safe Storage）+ AES-128-CBC，已实现
	//   Windows → Chrome 127+ 起用 App-Bound Encryption，密钥受 elevation 服务保护，
	//             需要调 IElevator COM 接口——是另一套工程
	//   Linux   → gnome-keyring / kwallet 各有差异
	// 与其给一个"看起来支持但实际报错"的假象，不如直接说清楚并给出可行的替代路径。
	if runtime.GOOS != "darwin" {
		label := runtime.GOOS
		switch runtime.GOOS {
		case "windows":
			label = "Windows"
		case "linux":
			label = "Linux"
		}
		return nil, "", fmt.Errorf(
			"自动读取浏览器登录态目前只支持 macOS（当前系统：%s）。\n"+
				"  你仍然可以使用本工具，改用 --cookie 手工提供登录态：\n"+
				"    1. 用浏览器打开 https://yuanbao.tencent.com 并扫码登录\n"+
				"    2. 按 F12 打开开发者工具 → Application/存储 → Cookies\n"+
				"    3. 把该域名下的 cookie 全部复制成 \"name=value; name2=value2\" 的形式\n"+
				"    4. 运行：wx-channels-transcript \"<链接>\" --cookie \"粘贴的内容\"\n"+
				"  详见 docs/troubleshooting.md", label)
	}

	browsers := DetectBrowsers()
	if len(browsers) == 0 {
		return nil, "", fmt.Errorf("没有找到任何 Chromium 系浏览器（Chrome / Edge / Brave / Arc …）。" +
			"如果你确实装了，可能是 cookie 库路径变了；也可以直接用 --cookie 手工提供登录态")
	}
	target := browsers[0]
	if browserName != "" {
		var ok bool
		for _, b := range browsers {
			if b.Name == browserName {
				target, ok = b, true
				break
			}
		}
		if !ok {
			return nil, "", fmt.Errorf("没有找到浏览器 %s", browserName)
		}
	}
	jar, err := target.ReadBrowserCookies(requestHost)
	if err != nil {
		return nil, target.Name, err
	}
	return jar, target.Name, nil
}
