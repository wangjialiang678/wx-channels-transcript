// Package asr 定义 ASR 后端的抽象、注册与选择。
//
// 选择原则（按「用户需要多做几步」排序，越少越优先）：
//  1. 云端 + 公网 URL 直传 + 能直接吃 mp4 → 零额外步骤（阿里云百炼、火山大模型）
//  2. 云端 + 公网 URL 直传 + 只收音频格式 → 需要 ffmpeg 抽音频
//  3. 本地模型                              → 需要下载 + ffmpeg + 本地算力
//
// 一句话：**优先云端**（快、准、便宜），本地作为没有凭证时的兜底。
package asr

import (
	"context"
	"fmt"
	"sort"
)

// Request 是一次转写请求。
type Request struct {
	URL          string   // 公网直链（与 File 二选一，URL 优先）
	File         string   // 本地文件路径
	Model        string   // 模型名，空则用后端默认
	Language     string   // 语言提示
	Hotwords     []string // 热词
	CorpusText   string   // 阿里侧的 corpus.text（含音近提示的整段文本）
	VocabularyID string   // 阿里侧预编译词表 ID
	WorkDir      string   // 本地后端的临时目录
	Env          map[string]string
}

// Result 是一次转写结果。
type Result struct {
	Text        string
	Model       string
	Sentences   []any
	Raw         any
	DurationSec float64 // 服务端返回的音频时长（拿不到时为 0）
}

// Backend 是一个语音识别后端。
type Backend interface {
	ID() string
	Label() string
	Models() []string
	DefaultModel() string
	AcceptsURL() bool  // 是否支持公网直链直传
	AcceptsMP4() bool  // 是否能直接吃 mp4（不需要 ffmpeg）
	NeedsFfmpeg() bool // 是否依赖本机 ffmpeg
	Detect(env map[string]string) Detection
	Transcribe(ctx context.Context, req Request, progress func(string)) (*Result, error)
}

// Detection 是「这个后端在本机能不能用」的探测结果。
type Detection struct {
	Available bool
	Detail    string            // 可用时的说明，如「使用 DASHSCOPE_API_KEY」
	Reason    string            // 不可用的原因
	Hint      string            // 如何配置（多行）
	Docs      string            // 官方文档链接
	Env       map[string]string // 探测到的凭证，直接透传给 Transcribe
}

// Registry 是全部后端实现。刻意保留 volcengine-small 的注册，
// 但它不参与自动推荐（见 Order）。
var Registry = map[string]Backend{}

func register(b Backend) { Registry[b.ID()] = b }

func init() {
	register(&bailian{})
	register(&volcengine{})
	register(volcengineSmall{})
	register(&local{})
}

// Order 是参与「自动探测 + 推荐」的后端顺序。
//
// 只收录**第二代（大模型 ASR）**：
//
//	bailian     qwen3-asr —— 默认，最快
//	volcengine  volc.seedasr.auc 2.0 —— 同级，热词直传更方便
//	local       whisper —— 数据不出本机的兜底
//
// 刻意排除的两条都是**第一代传统 ASR**（没有世界知识，专名会崩）：
// paraformer-v2（阿里，已从 bailian 的模型列表删除）与 volcengine-small
// （火山老体系，仍可用 -t volcengine-small 显式调用）。
var Order = []string{"bailian", "volcengine", "local"}

// GetBackend 按 id 取后端，未知 id 返回带指引的错误。
func GetBackend(id string) (Backend, error) {
	b, ok := Registry[id]
	if !ok {
		ids := make([]string, 0, len(Registry))
		for k := range Registry {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("未知的 ASR 后端：%s。可选：%v", id, ids)
	}
	return b, nil
}

// ExtraSteps 描述这个后端要跑通，用户总共需要额外做几步。
func ExtraSteps(b Backend) []string {
	var steps []string
	if !b.AcceptsURL() {
		steps = append(steps, "先把媒体下载到本地")
	}
	if !b.AcceptsMP4() {
		steps = append(steps, "用 ffmpeg 抽取音频")
	}
	return steps
}

// Detected 是一个后端在「本机」的探测结果。
type Detected struct {
	Backend Backend
	ID      string
	Label   string
	Detection
	ExtraSteps []string
	Score      int
	Notes      []string
}

// DetectAll 探测所有参与推荐的后端。
func DetectAll(env map[string]string) []Detected {
	out := make([]Detected, 0, len(Order))
	for _, id := range Order {
		b := Registry[id]
		var r Detection
		func() {
			defer func() {
				if e := recover(); e != nil {
					r = Detection{Available: false, Reason: fmt.Sprintf("探测出错：%v", e)}
				}
			}()
			r = b.Detect(env)
		}()
		out = append(out, Detected{
			Backend: b, ID: id, Label: b.Label(),
			Detection: r, ExtraSteps: ExtraSteps(b),
		})
	}
	return out
}

// Recommend 给出推荐：返回排序后的可用后端，并附上「为什么推荐它」的人话解释。
func Recommend(detected []Detected, hasFfmpeg bool) (chosen *Detected, ranked []Detected, reason string) {
	var usable []Detected
	for _, d := range detected {
		if d.Available {
			usable = append(usable, d)
		}
	}
	if len(usable) == 0 {
		return nil, nil, "本机没有检测到任何可用的 ASR 后端。"
	}

	scored := make([]Detected, 0, len(usable))
	for _, d := range usable {
		score := 0
		var notes []string
		if d.ID == "bailian" {
			score += 100 // 云端优先
		}
		if d.ID != "local" {
			score += 50
		}
		if d.Backend.AcceptsURL() {
			score += 30
			notes = append(notes, "支持公网直链直传，不用先下载")
		}
		if d.Backend.AcceptsMP4() {
			score += 25
			notes = append(notes, "能直接吃 mp4，不需要 ffmpeg")
		}
		if d.Backend.NeedsFfmpeg() && !hasFfmpeg {
			score -= 40
			notes = append(notes, "⚠️ 本机缺 ffmpeg，这条路会卡住")
		}
		if d.ID == "local" {
			score -= 60
			notes = append(notes, "本地推理，慢且吃算力")
		}
		d.Score, d.Notes = score, notes
		scored = append(scored, d)
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })

	top := scored[0]
	var why []string
	why = append(why, fmt.Sprintf("%s 已就绪（%s）。", top.Label, top.Detail))
	switch {
	case top.Backend.AcceptsURL() && top.Backend.AcceptsMP4():
		why = append(why, "它既能接受公网直链、又能直接处理 mp4，所以整条链路可以做到"+
			"「不下载、不转码」，是最快的一条。")
	case top.Backend.AcceptsURL():
		why = append(why, "它能接受公网直链，但只收音频格式，所以需要先用 ffmpeg 抽一次音频。")
	default:
		why = append(why, "它只能处理本地文件，所以需要先把媒体下载下来。")
	}
	if len(scored) > 1 {
		var alts []string
		for _, s := range scored[1:] {
			alts = append(alts, s.Label)
		}
		why = append(why, fmt.Sprintf("备选：%s。", joinChinese(alts)))
	}

	return &scored[0], scored, joinAll(why)
}

func joinAll(parts []string) string {
	out := ""
	for _, p := range parts {
		out += p
	}
	return out
}

func joinChinese(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += "、"
		}
		out += s
	}
	return out
}
