// Package output 负责结果落盘。
//
// 输出两份：
//   - `YYYY-MM-DD-<标题>.md`  —— 人读的逐字稿（带来源、时长等元信息）
//   - `<同名>.json`           —— 机器读的（含分句时间戳，便于做字幕或二次加工）
package output

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wangjialiang678/wx-channels-transcript/internal/jsonout"
)

// Timing 记录各阶段耗时（秒）。
type Timing struct {
	Resolve    float64 `json:"resolve"`
	Transcribe float64 `json:"transcribe"`
	Media      float64 `json:"media"`
	Total      float64 `json:"total"`
}

// Meta 是落盘需要的全部元信息。
type Meta struct {
	Title        string
	SourceURL    string
	ShareID      string
	ExportID     string
	MediaURL     string
	Author       string
	DurationSec  float64
	IsLocal      bool
	LocalPath    string
	BackendID    string
	BackendLabel string
	Model        string
	Timing       Timing
}

// Result 是落盘结果。
type Result struct {
	MDPath     string
	JSONPath   string
	TextLength int
}

var (
	slugUnsafeRe = regexp.MustCompile(`[\s#？?！!，,。.、：:；;“”"'（）()\[\]【】/\\|<>*]+`)
	slugDashRe   = regexp.MustCompile(`-+`)
)

// Slugify 把标题变成安全的文件名片段。
// 非法字符替换成 '-'，合并连续 '-'，去掉首尾 '-'，最后截断到 maxLen 个字符。
// 注意：截断在去首尾 '-' 之后进行，与 Node 版保持一致（截断点右侧若刚好是
// 连字符，会保留下来）。
func Slugify(title, fallback string, maxLen int) string {
	t := slugUnsafeRe.ReplaceAllString(title, "-")
	t = slugDashRe.ReplaceAllString(t, "-")
	t = strings.Trim(t, "-")
	r := []rune(t)
	if len(r) > maxLen {
		r = r[:maxLen]
	}
	t = string(r)
	if t == "" {
		return fallback
	}
	return t
}

// fmtDuration 把秒数格式化成人话；0 或负数视为未知。
func fmtDuration(sec float64) string {
	if sec <= 0 {
		return "未知"
	}
	s := int(sec + 0.5)
	if s < 60 {
		return fmt.Sprintf("%d 秒", s)
	}
	return fmt.Sprintf("%d 分 %d 秒", s/60, s%60)
}

// Write 写出 .md 与同名 .json。
func Write(outDir string, meta Meta, text string, sentences []any, raw any) (Result, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Result{}, err
	}
	// 用 UTC 日期，与 Node 版（toISOString().slice(0,10)）保持一致，
	// 否则同一天在两版之间会落到不同文件名上。
	stamp := time.Now().UTC().Format("2006-01-02")
	fallback := meta.ShareID
	if fallback == "" {
		fallback = "transcript"
	}
	base := stamp + "-" + Slugify(meta.Title, fallback, 32)
	mdPath := filepath.Join(outDir, base+".md")
	jsonPath := filepath.Join(outDir, base+".json")

	title := meta.Title
	if title == "" {
		title = "(无标题)"
	}
	dur := fmtDuration(meta.DurationSec)

	lines := []string{
		"---",
		"title: " + title,
		"source_url: " + meta.SourceURL,
		"share_id: " + meta.ShareID,
	}
	if meta.Author != "" {
		lines = append(lines, "author: "+meta.Author)
	}
	lines = append(lines,
		"duration: "+dur,
		"extracted_at: "+time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"asr_backend: "+meta.BackendID,
		"asr_model: "+meta.Model,
		fmt.Sprintf("elapsed_seconds: %.1f", meta.Timing.Total),
		"asr_note: ASR 原始输出，未人工校对，同音字可能有误",
		"---",
		"",
		"# "+title,
		"",
		"> 来源："+meta.SourceURL,
	)
	if meta.Author != "" {
		lines = append(lines, "> 作者："+meta.Author)
	}
	lines = append(lines,
		fmt.Sprintf("> 时长：%s　|　转写：%s　|　耗时：%.1f 秒", dur, meta.Model, meta.Timing.Total),
		">",
		"> 文字为语音识别原始输出，**未做人工校对**。要精确引用请回原视频核对。",
		"",
		"---",
		"",
		text,
		"",
	)

	if err := os.WriteFile(mdPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return Result{}, err
	}

	source := map[string]any{
		"shareId":  nullable(meta.ShareID),
		"exportId": nullable(meta.ExportID),
		"title":    meta.Title,
		"author":   nullable(meta.Author),
		"mediaUrl": nullable(meta.MediaURL),
		"url":      meta.SourceURL,
	}
	if meta.IsLocal {
		source["isLocal"] = true
		source["localPath"] = meta.LocalPath
	}
	payload := map[string]any{
		"source": source,
		"backend": map[string]any{
			"id": meta.BackendID, "label": meta.BackendLabel, "model": meta.Model,
		},
		"timing":    meta.Timing,
		"text":      text,
		"sentences": sentencesOrEmpty(sentences),
	}

	b, err := jsonout.MarshalIndent(payload)
	if err != nil {
		return Result{}, err
	}
	b = append(b, '\n')
	if err := os.WriteFile(jsonPath, b, 0o644); err != nil {
		return Result{}, err
	}

	return Result{MDPath: mdPath, JSONPath: jsonPath, TextLength: len([]rune(text))}, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func sentencesOrEmpty(s []any) []any {
	if s == nil {
		return []any{}
	}
	return s
}
