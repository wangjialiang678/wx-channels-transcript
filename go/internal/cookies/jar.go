package cookies

import (
	"sort"
	"strings"
)

// Jar 是一份「cookie 名 → 值」的集合。
//
// 值必须是**字节串**：浏览器里有些 cookie 是二进制（实测 analytics 类），
// 任何 UTF-8 校验或转换都会破坏它。Go 的 string 天然就是字节串，
// 这里只是把它当成不透明的字节序列来搬运。
type Jar map[string]string

// Header 把 jar 拼成 Cookie 请求头。
// 按键名排序，保证同一份 jar 每次产出完全一致的字符串（便于缓存比对与测试）。
func (j Jar) Header() string {
	keys := make([]string, 0, len(j))
	for k := range j {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+j[k])
	}
	return strings.Join(parts, "; ")
}

// Clone 复制一份 jar，避免调用方意外改到缓存里的对象。
func (j Jar) Clone() Jar {
	out := make(Jar, len(j))
	for k, v := range j {
		out[k] = v
	}
	return out
}

// Merge 返回 j 与 other 的并集，other 覆盖 j 的同名项。
func (j Jar) Merge(other Jar) Jar {
	out := j.Clone()
	for k, v := range other {
		out[k] = v
	}
	return out
}

// ParseHeader 解析 `a=1; b=2` 形式的 cookie 头。
func ParseHeader(header string) Jar {
	jar := make(Jar)
	for _, part := range strings.Split(header, ";") {
		i := strings.IndexByte(part, '=')
		if i > 0 {
			jar[strings.TrimSpace(part[:i])] = strings.TrimSpace(part[i+1:])
		}
	}
	return jar
}
