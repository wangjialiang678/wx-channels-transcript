// Package env 负责加载凭据。
//
// 除了真实的环境变量，还支持从几个常见的凭据文件里读——这样用户
// 不必为了用这个工具而改变自己既有的密钥管理方式。
package env

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 一行形如 `KEY=value`，允许 `export` 前缀。
var lineRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// 明显被停用的占位值，直接跳过。
var disabledRe = regexp.MustCompile(`(?i)^(DISABLED|TODO|xxx|your[_-]?)`)

// DefaultFiles 返回默认会尝试读取的凭据文件（存在才读，后者覆盖前者）。
func DefaultFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude/api-vault.env"),
		filepath.Join(home, ".config/sph-transcript/.env"),
		filepath.Join(home, ".env"),
	}
}

// ExistingFiles 过滤出实际存在的凭据文件，用于 scan 输出。
func ExistingFiles(files []string) []string {
	if files == nil {
		files = DefaultFiles()
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// parseFile 读取一个 KEY=value 文件，把结果合并进 dst。
// 文件不存在或不可读时静默跳过（凭据文件是可选的）。
func parseFile(path string, dst map[string]string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[2])
		// 成对引号包裹时剥掉引号（两种引号都支持）
		if len(v) >= 2 && v[0] == v[len(v)-1] && (v[0] == '"' || v[0] == '\'') {
			v = v[1 : len(v)-1]
		}
		if disabledRe.MatchString(v) {
			continue
		}
		dst[m[1]] = v
	}
}

// Load 合并凭据文件与真实环境变量：文件在前，真实环境变量覆盖之
// （这样用户临时 export 一个值就能压过文件里的旧值）。
func Load(files []string) map[string]string {
	if files == nil {
		files = DefaultFiles()
	}
	merged := make(map[string]string)
	for _, f := range files {
		parseFile(f, merged)
	}
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		merged[kv[:i]] = kv[i+1:]
	}
	return merged
}
