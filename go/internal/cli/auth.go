package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/wangjialiang678/wx-channels-transcript/internal/cookies"
	"github.com/wangjialiang678/wx-channels-transcript/internal/session"
)

// cmdAuth 查看或清除登录态缓存。
func cmdAuth(f *Flags) int {
	s := session.New("")

	if f.Bool("clear") {
		if err := s.ClearCache(); err != nil {
			stderr("清除登录态缓存失败：%v", err)
			return ExitBadArgs
		}
		stdout("已清除登录态缓存：%s", s.Path())
		return ExitOK
	}

	cached := s.LoadCache()
	if cached == nil {
		stdout("没有登录态缓存。下次提取时会自动读取浏览器并弹一次钥匙串授权。")
		return ExitOK
	}

	age := "?"
	if t, err := time.Parse(time.RFC3339, cached.SavedAt); err == nil {
		age = fmt.Sprintf("%.1f", time.Since(t).Hours()/24)
	}
	stdout("登录态缓存：%s", s.Path())
	stdout("保存时间：%s（%s 天前）", cached.SavedAt, age)
	stdout("cookie 条数：%d", len(cached.Jar))
	stdout("")
	stdout("校验中…")

	v, err := s.Verify(context.Background(), cookies.Jar(cached.Jar).Header())
	if err != nil {
		stdout("❌ 校验失败：%v", err)
		return ExitAuth
	}
	if v.OK {
		stdout("✅ 仍然有效")
	} else {
		stdout("❌ 已失效（HTTP %d），下次会自动重新读取浏览器", v.Status)
	}
	return ExitOK
}
