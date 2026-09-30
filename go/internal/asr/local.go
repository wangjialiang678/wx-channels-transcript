package asr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ─────────────────────────────────────────────── 本地模型（whisper.cpp）
//
// 定位：**没有云端凭证时的兜底**，以及处理敏感素材时想完全离线。
// 代价要说清楚：需要自己装推理引擎 + 下模型，且本地推理比云端慢得多。
//
// 走「外部可执行文件」路线而不是进程内推理，是为了保持单二进制的干净——
// 不把几 GB 的推理运行时链进产物里。
const localTimeout = 60 * time.Minute

// 已知的本地推理引擎可执行文件名（按优先级）。
var whisperCLINames = []string{"whisper-cli", "whisper-cpp", "whisper"}

type local struct{}

func (*local) ID() string           { return "local" }
func (*local) Label() string        { return "本地模型（whisper.cpp）" }
func (*local) Models() []string     { return []string{"whisper-small", "whisper-medium", "sensevoice"} }
func (*local) DefaultModel() string { return "whisper-small" }
func (*local) AcceptsMP4() bool     { return false } // 必须先抽成 16kHz wav
func (*local) AcceptsURL() bool     { return false } // 只能吃本地文件
func (*local) NeedsFfmpeg() bool    { return true }

func modelHints() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".cache/whisper/ggml-small.bin"),
		filepath.Join(home, ".cache/whisper/ggml-base.bin"),
		filepath.Join(home, ".cache/whisper/ggml-medium.bin"),
		filepath.Join(home, "models/ggml-small.bin"),
	}
}

func findWhisperCLI() string {
	for _, name := range whisperCLINames {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func findWhisperModel() string {
	if m := os.Getenv("WHISPER_MODEL"); m != "" {
		if _, err := os.Stat(m); err == nil {
			return m
		}
	}
	for _, m := range modelHints() {
		if _, err := os.Stat(m); err == nil {
			return m
		}
	}
	// 兜底：扫 ~/.cache/whisper 下的 ggml-*.bin
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".cache/whisper")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) > 5 && name[:5] == "ggml-" && filepath.Ext(name) == ".bin" {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

func (*local) Detect(env map[string]string) Detection {
	cli := findWhisperCLI()
	if cli == "" {
		return Detection{
			Available: false,
			Reason:    "没有找到本地 whisper 可执行文件",
			Hint: "安装方式二选一：\n" +
				"    brew install whisper-cpp            （macOS，推荐）\n" +
				"    或参考 https://github.com/ggml-org/whisper.cpp 自行编译\n" +
				"  然后下载模型（如 ggml-small.bin，约 466MB）放到 ~/.cache/whisper/。",
			Docs: "https://github.com/ggml-org/whisper.cpp",
		}
	}
	model := findWhisperModel()
	if model == "" {
		return Detection{
			Available: false,
			Reason:    fmt.Sprintf("找到了 %s，但没有找到模型文件", cli),
			Hint: "下载模型放到 ~/.cache/whisper/：\n" +
				"    curl -L -o ~/.cache/whisper/ggml-small.bin \\\n" +
				"      https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin\n" +
				"  或用环境变量 WHISPER_MODEL 指定已有模型路径。",
			Docs: "https://github.com/ggml-org/whisper.cpp#models",
		}
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return Detection{
			Available: false,
			Reason:    "本地后端需要 ffmpeg 抽音频，但没有找到",
			Hint:      "brew install ffmpeg",
			Docs:      "https://ffmpeg.org/",
		}
	}
	return Detection{
		Available: true,
		Detail:    fmt.Sprintf("%s + %s", cli, model),
		Env:       map[string]string{"cli": cli, "model": model},
	}
}

// Transcribe 只接受本地文件。上层负责先把媒体下下来。
func (*local) Transcribe(ctx context.Context, req Request, progress func(string)) (*Result, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if req.File == "" {
		return nil, fmt.Errorf("local: 需要提供本地文件路径")
	}
	if _, err := os.Stat(req.File); err != nil {
		return nil, fmt.Errorf("local: 找不到文件 %s", req.File)
	}
	cli := req.Env["cli"]
	model := req.Model
	if model == "" {
		model = req.Env["model"]
	}
	workDir := req.WorkDir
	if workDir == "" {
		workDir = os.TempDir()
	}

	// 抽成 16kHz 单声道 WAV —— whisper.cpp 只吃这个规格
	wav := filepath.Join(workDir, fmt.Sprintf("sph-local-%d.wav", time.Now().UnixNano()))
	progress("ffmpeg 抽取 16kHz 单声道音频…")
	ff, err := exec.CommandContext(ctx, "ffmpeg", "-y", "-v", "error",
		"-i", req.File, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("local: 抽音频失败 %s", firstNStr(string(ff), 300))
	}
	if _, err := os.Stat(wav); err != nil {
		return nil, fmt.Errorf("local: 抽音频失败，输出文件不存在")
	}
	defer os.Remove(wav)

	progress("本地推理中（首次会很慢）…")
	outPrefix := filepath.Join(workDir, fmt.Sprintf("sph-local-%d", time.Now().UnixNano()))
	lang := "zh"
	if req.Language != "" && req.Language != "zh" {
		lang = "auto"
	}
	args := []string{"-m", model, "-f", wav, "-l", lang, "-otxt", "-of", outPrefix, "--no-prints"}

	// whisper.cpp 没有原生热词，用初始提示词（--prompt）近似引导。
	// 注意它有 ~224 token 上限，超出会被截断，所以这里硬截一刀。
	prompt := req.CorpusText
	if prompt == "" {
		prompt = joinChinese(req.Hotwords)
	}
	if prompt != "" {
		prompt = truncateRunes(prompt, 400)
		args = append(args, "--prompt", prompt)
		progress(fmt.Sprintf("携带初始提示词（%d 字符，近似热词）", len([]rune(prompt))))
	}

	runCtx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	out, err := exec.CommandContext(runCtx, cli, args...).CombinedOutput()
	txtPath := outPrefix + ".txt"
	if err != nil {
		if _, statErr := os.Stat(txtPath); statErr != nil {
			return nil, fmt.Errorf("local: 推理失败 %s", firstNStr(string(out), 300))
		}
	}
	b, err := os.ReadFile(txtPath)
	if err != nil {
		return nil, fmt.Errorf("local: 读取推理结果失败：%w", err)
	}
	_ = os.Remove(txtPath)
	text := strings.TrimSpace(string(b))
	if text == "" {
		return nil, fmt.Errorf("local: 推理结果为空")
	}
	return &Result{
		Text:      text,
		Model:     "whisper:" + filepath.Base(model),
		Sentences: []any{},
	}, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
