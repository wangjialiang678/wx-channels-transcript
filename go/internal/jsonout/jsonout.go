// Package jsonout 提供与 Node 版一致的 JSON 序列化。
//
// Go 的 encoding/json 默认会把 < > & 转义成 \u003c 之类，Node 的
// JSON.stringify 不会。本工具的产出物要跟 Node 版逐字节对得上，
// 所以统一关掉 HTML 转义。
package jsonout

import (
	"bytes"
	"encoding/json"
)

// MarshalIndent 序列化为缩进 JSON，不转义 HTML 字符。
func MarshalIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encoder.Encode 会补一个换行，去掉它由调用方决定要不要加
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
