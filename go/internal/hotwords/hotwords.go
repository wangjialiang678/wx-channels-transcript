// Package hotwords 负责热词（自定义词表）的提取与传递。
//
// 为什么需要它：ASR 最容易错的是**英文专名和中文同音字**——
// 实测同一段音频，模型会把「苏姿丰」写成「朱志峰」。喂进热词后即可纠正。
//
// 核心思路：**视频号标题里往往就写着关键专名**。例如
//
//	82亿美元，苏姿丰买下李飞飞的 WorldLabs。#李飞飞 #苏姿丰 #AMD #人工智能 #AI
//
// 从中能自动提取出 李飞飞 / 苏姿丰 / AMD / WorldLabs。
package hotwords

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

const (
	// DefaultMax 是自动提取热词的条数上限。
	DefaultMax = 50
	// CorpusIntro 是 corpus.text 的开场白。
	CorpusIntro = "本次录音涉及以下专有名词。如果听到与括号内读音相近的内容，请输出括号外的正确写法："
)

var (
	hashtagRe    = regexp.MustCompile(`#([^\s#，,。.、；;！!？?|]+)`)
	upperWordRe  = regexp.MustCompile(`\b([A-Z][A-Za-z0-9]{1,})\b`)
	splitArgRe   = regexp.MustCompile(`[,，\s]+`)
	disabledWord = map[string]bool{
		"AI": true, "A": true, "I": true, "THE": true, "AND": true, "OF": true,
		"TO": true, "IN": true, "IS": true,
		"人工智能": true, "科技": true, "前沿": true, "科技前沿": true,
		"视频": true, "推荐": true, "热门": true, "分享": true,
	}
)

// Extract 从标题里提取候选热词。
func Extract(title string, extra []string) []string {
	seen := make(map[string]bool)
	var words []string
	add := func(w string) {
		w = strings.TrimSpace(w)
		if w == "" || seen[w] {
			return
		}
		seen[w] = true
		words = append(words, w)
	}

	// 1) 话题标签：#李飞飞 #AMD #WorldLabs
	for _, m := range hashtagRe.FindAllStringSubmatch(title, -1) {
		add(m[1])
	}
	// 2) 首字母大写 + 后续字母数字（≥2 字符）：AMD、ChatGPT、ImageNet、WorldLabs
	for _, m := range upperWordRe.FindAllStringSubmatch(title, -1) {
		add(m[1])
	}
	// 3) 用户额外指定
	for _, w := range extra {
		add(w)
	}

	out := make([]string, 0, len(words))
	for _, w := range words {
		if len([]rune(w)) < 2 {
			continue
		}
		if disabledWord[w] || disabledWord[strings.ToUpper(w)] {
			continue
		}
		out = append(out, w)
		if len(out) >= DefaultMax {
			break
		}
	}
	return out
}

// ParseArg 解析 --hotwords "词1,词2" 或 "词1 词2" 或文件路径（每行一个 / 逗号分隔）。
func ParseArg(arg string) []string {
	s := strings.TrimSpace(arg)
	if s == "" {
		return nil
	}
	if st, err := os.Stat(s); err == nil && !st.IsDir() {
		b, err := os.ReadFile(s)
		if err == nil {
			var out []string
			for _, part := range strings.FieldsFunc(string(b), func(r rune) bool {
				return r == '\n' || r == ','
			}) {
				if t := strings.TrimSpace(part); t != "" {
					out = append(out, t)
				}
			}
			return out
		}
	}
	var out []string
	for _, part := range splitArgRe.Split(s, -1) {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Unique 去重并保持顺序。
func Unique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, w := range in {
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// BuildCorpusText 构造送给阿里的 corpus.text。
//
// 阿里对这段文本是**高度容错**的（官方原话：「甚至无意义的文本也不会影响识别」），
// 所以可以写得比纯词表更有信息量——在热词后面附加音近提示，
// 引导模型输出正确拼写，例如「Qwen3（千问三／千万三）」。
func BuildCorpusText(words []string) string {
	if len(words) == 0 {
		return ""
	}
	return CorpusIntro + "\n" + strings.Join(words, "、")
}

// VolcengineContext 构造火山侧 request.corpus.context 的 JSON 字符串。
func VolcengineContext(words []string) string {
	if len(words) == 0 {
		return ""
	}
	type item struct {
		Word string `json:"word"`
	}
	items := make([]item, 0, len(words))
	for _, w := range words {
		items = append(items, item{Word: w})
	}
	b, err := json.Marshal(map[string]any{"hotwords": items})
	if err != nil {
		return ""
	}
	return string(b)
}
