package asr

import "encoding/json"

// 这里放几个宽松取值的 JSON 小工具。
// 这些接口都是非公开/半公开的，字段名随时可能变，所以取值一律走「取不到就当没有」。

func obj(m any, key string) map[string]any {
	if mm, ok := m.(map[string]any); ok {
		if v, ok := mm[key].(map[string]any); ok {
			return v
		}
	}
	return nil
}

func arr(m any, key string) []any {
	if mm, ok := m.(map[string]any); ok {
		if v, ok := mm[key].([]any); ok {
			return v
		}
	}
	return nil
}

func str(m any, key string) string {
	if mm, ok := m.(map[string]any); ok {
		if v, ok := mm[key].(string); ok {
			return v
		}
	}
	return ""
}

// num 取数值字段；JSON 数字统一是 float64。
func num(m any, key string) (float64, bool) {
	if mm, ok := m.(map[string]any); ok {
		if v, ok := mm[key].(float64); ok {
			return v, true
		}
	}
	return 0, false
}

// nonEmpty 返回第一个非空字符串。
func nonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// truncateJSONRaw 把任意 JSON 值序列化后截断，用于错误信息里展示服务端原始返回。
func truncateJSONRaw(v any, n int) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<无法序列化>"
	}
	return firstNStr(string(b), n)
}

// msToSeconds 把毫秒时长换算成秒；明显不合理的值（<=0 或过长）当作未知。
func msToSeconds(ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	sec := ms / 1000
	if sec > 24*3600 {
		return 0
	}
	return sec
}

// lastSentenceEnd 从分句里推出总时长（各家分句的 end_time 都是毫秒）。
// 服务端没给总时长时的兜底。
func lastSentenceEnd(sentences []any) float64 {
	var maxEnd float64
	for _, s := range sentences {
		if v, ok := num(s, "end_time"); ok && v > maxEnd {
			maxEnd = v
		}
	}
	return msToSeconds(maxEnd)
}
