// Package media 负责下载媒体与抽取音频。
//
// 设计原则：**每一步都按需触发，能省就省**。
//   - 选中的 ASR 支持公网直链 + 能吃 mp4（如阿里云）→ 本包完全不被调用
//   - 后端只收音频 → 下载 + 抽音频
//   - 用户显式要音频/视频（--save-audio / --save-video）→ 即使后端不需要，也照做
package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Format 描述一种音频输出格式。
type Format struct {
	Ext  string
	Args []string
}

// AudioFormats 是支持的音频格式；Order 保证报错信息里的顺序稳定。
var AudioFormats = map[string]Format{
	"wav": {"wav", []string{"-c:a", "pcm_s16le"}},
	"mp3": {"mp3", []string{"-c:a", "libmp3lame", "-q:a", "4"}},
	"m4a": {"m4a", []string{"-c:a", "aac", "-b:a", "128k"}},
}

// AudioFormatOrder 是 AudioFormats 的稳定顺序。
var AudioFormatOrder = []string{"wav", "mp3", "m4a"}

const (
	downloadTimeout = 30 * time.Minute
	ffmpegTimeout   = 30 * time.Minute
)

// HasFfmpeg 报告本机是否装了 ffmpeg。
func HasFfmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func ffmpegBin() (string, error) {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("需要 ffmpeg 但本机没有安装。\n" +
			"  安装：brew install ffmpeg\n" +
			"  或者改用「能直链直传且能吃 mp4」的云端后端（如阿里云百炼），可以完全跳过这一步")
	}
	return p, nil
}

// Download 把媒体下到本地。直链自带签名，不需要任何 cookie。
func Download(ctx context.Context, mediaURL, destPath string, progress func(string)) (int64, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, err
	}
	progress("下载媒体…")

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("下载失败 HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	size, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("写入媒体文件失败：%w", err)
	}
	progress(fmt.Sprintf("已下载 %.1f MB", float64(size)/1048576))
	return size, nil
}

// ExtractAudio 抽取音频。默认转 16kHz 单声道（ASR 通用规格）；
// forASR 为 false 时保留原始采样率与声道（用户自己要留音频的场景）。
func ExtractAudio(ctx context.Context, srcPath, destPath, format string, forASR bool, progress func(string)) (int64, error) {
	if progress == nil {
		progress = func(string) {}
	}
	spec, ok := AudioFormats[format]
	if !ok {
		spec = AudioFormats["wav"]
	}
	label := format
	if forASR {
		label += "，16kHz 单声道"
	}
	progress("抽取音频（" + label + "）…")

	bin, err := ffmpegBin()
	if err != nil {
		return 0, err
	}
	args := []string{"-y", "-v", "error", "-i", srcPath, "-vn"}
	if forASR {
		args = append(args, "-ac", "1", "-ar", "16000") // ASR 要求：单声道 16k
	}
	args = append(args, spec.Args...)
	args = append(args, destPath)

	ctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("抽音频失败：%s", firstN(string(out), 300))
	}
	st, err := os.Stat(destPath)
	if err != nil {
		return 0, fmt.Errorf("抽音频失败：输出文件不存在")
	}
	return st.Size(), nil
}

// Options 是 PrepareMedia 的入参。
type Options struct {
	WorkDir      string
	NeedDownload bool
	NeedAudio    bool
	AudioFormat  string
}

// Local 是准备好之后的本地媒体路径。
type Local struct {
	MediaPath string
	AudioPath string
}

// PrepareMedia 下载（按需）并抽音频（按需），返回本地中间文件路径。
func PrepareMedia(ctx context.Context, mediaURL string, opts Options, progress func(string)) (Local, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if opts.AudioFormat == "" {
		opts.AudioFormat = "wav"
	}
	if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
		return Local{}, err
	}
	stamp := time.Now().UnixNano()
	var result Local

	if opts.NeedDownload || opts.NeedAudio {
		if mediaURL == "" {
			return Local{}, fmt.Errorf("需要下载媒体但拿不到直链（本地文件模式请走另一条路径）")
		}
		mediaPath := filepath.Join(opts.WorkDir, fmt.Sprintf("source-%d.mp4", stamp))
		if _, err := Download(ctx, mediaURL, mediaPath, progress); err != nil {
			return Local{}, err
		}
		result.MediaPath = mediaPath
	}

	if opts.NeedAudio {
		spec := AudioFormats[opts.AudioFormat]
		audioPath := filepath.Join(opts.WorkDir, fmt.Sprintf("audio-%d.%s", stamp, spec.Ext))
		// ASR 需要 16k 单声道；非 wav 格式保留给用户，不走 ASR 规格
		if _, err := ExtractAudio(ctx, result.MediaPath, audioPath, opts.AudioFormat, opts.AudioFormat == "wav", progress); err != nil {
			return result, err
		}
		result.AudioPath = audioPath
	}

	return result, nil
}

// Promote 把文件搬到输出目录（保留时用），保留原扩展名。
func Promote(srcPath, outDir, baseName string) (string, error) {
	if srcPath == "" {
		return "", nil
	}
	if _, err := os.Stat(srcPath); err != nil {
		return "", nil
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(srcPath)
	dest := filepath.Join(outDir, baseName+ext)
	if err := copyFile(srcPath, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// Cleanup 只删调用方明确点名、且确实存在的路径。
func Cleanup(paths []string) {
	for _, p := range paths {
		if p != "" {
			_ = os.Remove(p)
		}
	}
}

// CleanupDir 删除整个临时工作目录。
func CleanupDir(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
