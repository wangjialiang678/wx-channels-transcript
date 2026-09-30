package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wangjialiang678/wx-channels-transcript/internal/asr"
	"github.com/wangjialiang678/wx-channels-transcript/internal/hotwords"
	"github.com/wangjialiang678/wx-channels-transcript/internal/jsonout"
	"github.com/wangjialiang678/wx-channels-transcript/internal/media"
	"github.com/wangjialiang678/wx-channels-transcript/internal/output"
	"github.com/wangjialiang678/wx-channels-transcript/internal/resolve"
	"github.com/wangjialiang678/wx-channels-transcript/internal/session"
)

var httpURLRe = regexp.MustCompile(`(?i)^https?://`)

// cmdExtract 是主流程：登录态 → 解析 → 热词 → 转写 → 落盘。
func cmdExtract(f *Flags, e map[string]string) int {
	ctx := context.Background()
	t0 := time.Now()
	var timing output.Timing

	// ── 输入判定：链接 or 本地文件
	input := f.Positional[0]
	isURL := httpURLRe.MatchString(input)
	localFile := ""
	if !isURL {
		localFile = input
	} else if resolve.ParseShareID(input) == "" {
		stderr("错误：这不是可识别的视频号链接：%s", input)
		stderr("  期望形如 https://weixin.qq.com/sph/xxxx")
		return ExitBadArgs
	}
	if !isURL {
		if _, err := os.Stat(localFile); err != nil {
			stderr("错误：既不是 http(s) 链接，本地也找不到这个文件：%s", localFile)
			return ExitBadArgs
		}
	}

	// ── 输出相关选项
	outDir := f.Value("out", "o")
	if outDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			stderr("错误：取不到当前目录：%v", err)
			return ExitBadArgs
		}
		outDir = filepath.Join(cwd, "transcripts")
	}
	wantAudio := f.Bool("save-audio") || f.Bool("keep")
	wantVideo := f.Bool("save-video") || f.Bool("keep")
	audioFormat := f.Value("audio-format")
	if audioFormat == "" {
		audioFormat = "wav"
	}
	if _, ok := media.AudioFormats[audioFormat]; !ok {
		stderr("错误：不支持的音频格式 %s。可选：%s", audioFormat, strings.Join(media.AudioFormatOrder, " / "))
		return ExitBadArgs
	}

	// 中间文件放系统临时目录，保证默认零污染
	workDir, err := os.MkdirTemp("", "sph-")
	if err != nil {
		stderr("错误：创建临时目录失败：%v", err)
		return ExitBadArgs
	}
	defer media.CleanupDir(workDir)
	var tmpPaths []string
	defer func() {
		if !f.Bool("keep") {
			media.Cleanup(tmpPaths)
		}
	}()

	progress := func(m string) { stderr("   %s", m) }

	// ── ① 登录态 + ② 解析
	var info resolve.Info
	sess := session.New("")
	if isURL {
		stderr("① 获取元宝登录态…")
		got, err := sess.Acquire(ctx, session.AcquireOptions{
			Cookie:       f.Value("cookie"),
			Browser:      f.Value("browser"),
			AllowBrowser: !f.Bool("no-browser"),
			OnNotice:     func(msg string) { stderr("%s", msg) },
		})
		if err != nil {
			stderr("\n✗ %v", err)
			return ExitAuth
		}
		switch {
		case got.Origin == "cache":
			stderr("   使用缓存的登录态（未触碰浏览器）")
		case got.Origin == "arg":
			stderr("   使用 --cookie 提供的登录态")
		default:
			stderr("   已从 %s 读取（%d 条 cookie）并缓存", got.Browser, got.Count)
		}

		stderr("② 解析视频号链接…")
		tR := time.Now()
		info, err = resolve.Resolve(ctx, input, sess.CookieHeader())
		if err != nil {
			var ae *resolve.AuthError
			if errors.As(err, &ae) {
				_ = sess.ClearCache()
				stderr("\n✗ %v", err)
				stderr("  登录态已失效，缓存已清除。重跑一次会自动重新读取浏览器。")
				return ExitAuth
			}
			stderr("\n✗ %v", err)
			return ExitResolveFail
		}
		timing.Resolve = secSince(tR)
		author := ""
		if info.Author != "" {
			author = "　@" + info.Author
		}
		stderr("   ✓ %s%s", orDefault(info.Title, "(无标题)"), author)
	} else {
		base := filepath.Base(localFile)
		info = resolve.Info{
			URL:       localFile,
			Title:     strings.TrimSuffix(base, filepath.Ext(base)),
			IsLocal:   true,
			LocalPath: localFile,
		}
		stderr("① 本地文件模式：%s（跳过登录态与解析）", base)
	}

	// ── --probe：只解析拿直链
	if f.Bool("probe") {
		if isURL {
			payload := map[string]any{
				"title": info.Title, "author": info.Author,
				"shareId": info.ShareID, "exportId": info.ExportID, "mediaUrl": info.MediaURL,
			}
			if f.Bool("json") {
				stdout("%s", mustJSON(payload))
			} else {
				stdout("%s", info.MediaURL)
			}
		} else {
			if f.Bool("json") {
				stdout("%s", mustJSON(map[string]any{"isLocal": true, "localPath": localFile}))
			} else {
				stdout("%s", localFile)
			}
		}
		return ExitOK
	}

	// ── ②.5 热词：默认从视频标题里自动提取（标题往往就写着关键专名）
	var autoHot []string
	if !f.Bool("no-auto-hotwords") {
		autoHot = hotwords.Extract(info.Title, nil)
	}
	extraHot := hotwords.ParseArg(f.Value("hotwords"))
	allHot := hotwords.Unique(append(append([]string{}, autoHot...), extraHot...))

	if f.Bool("show-hotwords") {
		stdout("%s", mustJSON(map[string]any{
			"fromTitle": nonNil(autoHot),
			"fromArg":   nonNil(extraHot),
			"merged":    nonNil(allHot),
		}))
		return ExitOK
	}
	if len(allHot) > 0 {
		src := ""
		switch {
		case len(autoHot) > 0 && len(extraHot) > 0:
			src = "标题+参数"
		case len(autoHot) > 0:
			src = "视频标题"
		default:
			src = "--hotwords 参数"
		}
		shown := allHot
		suffix := ""
		if len(shown) > 10 {
			shown, suffix = shown[:10], "…"
		}
		stderr("   ✓ 热词 %d 个（来自%s）：%s%s", len(allHot), src, strings.Join(shown, "、"), suffix)
	}

	// ── ③ 选后端
	hasFFmpeg := media.HasFfmpeg()
	detected := asr.DetectAll(e)
	chosen, _, _ := asr.Recommend(detected, hasFFmpeg)

	var backend asr.Backend
	var backendEnv map[string]string
	if chosen != nil {
		backend, backendEnv = chosen.Backend, chosen.Detection.Env
	}
	if to := f.Value("to", "t"); to != "" {
		b, err := asr.GetBackend(to)
		if err != nil {
			stderr("\n✗ %v", err)
			stderr("  用 sph backends 看各后端的配置方法。")
			return ExitNoBackend
		}
		d := findDetected(detected, b.ID())
		if d == nil || !d.Available {
			reason := "未知原因"
			if d != nil {
				reason = d.Reason
			}
			stderr("\n✗ 指定的后端 %s 在本机不可用：%s", b.ID(), reason)
			stderr("  用 sph backends 看各后端的配置方法。")
			return ExitNoBackend
		}
		backend, backendEnv = b, d.Detection.Env
	}
	if backend == nil {
		stderr("\n✗ 本机没有可用的 ASR 后端，无法转写。")
		stderr("  跑 sph scan 看缺什么、怎么配。")
		return ExitNoBackend
	}

	// ── ④ 决定是否需要本地媒体 —— 这是「按需」的关键
	needDownloadForAsr := !backend.AcceptsURL() // 后端不接受直链 → 必须下载
	needAudioForAsr := !backend.AcceptsMP4()    // 后端不吃 mp4 → 必须抽音频
	needLocalMedia := needDownloadForAsr || needAudioForAsr || wantAudio || wantVideo

	stderr("③ 转写（%s）…", backend.Label())
	switch {
	case !needDownloadForAsr && !needAudioForAsr:
		stderr("   该后端支持直链直传且能直接处理 mp4 —— 不下载、不转码")
	case needAudioForAsr && !needDownloadForAsr:
		stderr("   该后端不吃 mp4 —— 需要先抽音频")
	default:
		stderr("   该后端只接受本地文件 —— 需要先下载")
	}
	if wantAudio || wantVideo {
		var parts []string
		if wantAudio {
			parts = append(parts, "音频("+audioFormat+")")
		}
		if wantVideo {
			parts = append(parts, "原始视频")
		}
		stderr("   另外你要求保留%s", strings.Join(parts, "和"))
	}

	var localMedia media.Local
	if needLocalMedia {
		tM := time.Now()
		if info.IsLocal {
			localMedia.MediaPath = info.LocalPath
			if needAudioForAsr || wantAudio {
				ap := audioPathIn(workDir, audioFormat)
				if _, err := media.ExtractAudio(ctx, info.LocalPath, ap, audioFormat, audioFormat == "wav", progress); err != nil {
					stderr("\n✗ 准备媒体失败：%v", err)
					return ExitResolveFail
				}
				localMedia.AudioPath = ap
			}
		} else {
			lm, err := media.PrepareMedia(ctx, info.MediaURL, media.Options{
				WorkDir:      workDir,
				NeedDownload: needDownloadForAsr || wantVideo || needAudioForAsr || wantAudio,
				NeedAudio:    needAudioForAsr || wantAudio,
				AudioFormat:  audioFormat,
			}, progress)
			if err != nil {
				stderr("\n✗ 准备媒体失败：%v", err)
				return ExitResolveFail
			}
			localMedia = lm
		}
		for _, p := range []string{localMedia.MediaPath, localMedia.AudioPath} {
			if p != "" && p != info.LocalPath {
				tmpPaths = append(tmpPaths, p)
			}
		}
		timing.Media = secSince(tM)
	}

	// ── ⑤ 转写
	tT := time.Now()
	req := asr.Request{
		Model:        f.Value("model", "m"),
		Language:     orDefault(f.Value("lang", "l"), "zh"),
		Hotwords:     allHot,
		CorpusText:   hotwords.BuildCorpusText(allHot),
		VocabularyID: f.Value("vocabulary-id"),
		WorkDir:      workDir,
		Env:          backendEnv,
	}

	var res *asr.Result
	var err2 error
	switch {
	case info.IsLocal:
		// 本地文件：不走下载，直接把文件交给后端。
		// ⚠️ 阿里支持上传本地文件（临时上传通道）；火山只接受公网 URL，会明确报错。
		req.File = info.LocalPath
		res, err2 = backend.Transcribe(ctx, req, progress)
	case backend.AcceptsURL() && !needAudioForAsr:
		// 最快路径：直链直传
		req.URL = info.MediaURL
		res, err2 = backend.Transcribe(ctx, req, progress)
	case backend.AcceptsURL() && localMedia.AudioPath != "":
		// 有直链但后端要音频格式：把刚抽好的音频传上去
		req.File = localMedia.AudioPath
		res, err2 = backend.Transcribe(ctx, req, progress)
	default:
		// 本地后端
		req.File = localMedia.AudioPath
		if req.File == "" {
			req.File = localMedia.MediaPath
		}
		if req.Model == "" {
			req.Model = backendEnv["model"]
		}
		res, err2 = backend.Transcribe(ctx, req, progress)
	}
	if err2 != nil {
		stderr("\n✗ 转写失败：%v", err2)
		return ExitTranscribe
	}
	timing.Transcribe = secSince(tT)
	timing.Total = secSince(t0)
	info.DurationSec = res.DurationSec

	// ── ⑥ 落盘
	written, err := output.Write(outDir, output.Meta{
		Title:        info.Title,
		SourceURL:    info.URL,
		ShareID:      info.ShareID,
		ExportID:     info.ExportID,
		MediaURL:     info.MediaURL,
		Author:       info.Author,
		DurationSec:  info.DurationSec,
		IsLocal:      info.IsLocal,
		LocalPath:    info.LocalPath,
		BackendID:    backend.ID(),
		BackendLabel: backend.Label(),
		Model:        res.Model,
		Timing:       timing,
	}, res.Text, res.Sentences, res.Raw)
	if err != nil {
		stderr("\n✗ 写文件失败：%v", err)
		return ExitTranscribe
	}

	// ── ⑦ 按需保留媒体
	baseName := strings.TrimSuffix(filepath.Base(written.MDPath), ".md")
	var kept []string
	if wantAudio && localMedia.AudioPath != "" {
		if p, err := media.Promote(localMedia.AudioPath, outDir, baseName); err == nil && p != "" {
			kept = append(kept, p)
		}
	}
	if wantVideo && localMedia.MediaPath != "" && localMedia.MediaPath != info.LocalPath {
		if p, err := media.Promote(localMedia.MediaPath, outDir, baseName); err == nil && p != "" {
			kept = append(kept, p)
		}
	}

	// ── ⑧ 收尾输出
	stderr("")
	mediaPart := ""
	if timing.Media > 0 {
		mediaPart = fmt.Sprintf(" + 媒体 %.1fs", timing.Media)
	}
	stderr("✓ 完成：%d 字，解析 %.1fs%s + 转写 %.1fs = %.1fs",
		written.TextLength, timing.Resolve, mediaPart, timing.Transcribe, timing.Total)
	stderr("  逐字稿：%s", written.MDPath)
	stderr("  JSON  ：%s", written.JSONPath)
	for _, k := range kept {
		stderr("  已保留：%s", k)
	}

	if f.Bool("json") {
		stdout("%s", mustJSON(map[string]any{
			"ok": true, "mdPath": written.MDPath, "jsonPath": written.JSONPath,
			"backend": backend.ID(), "model": res.Model,
			"textLength": written.TextLength, "timing": timing, "kept": nonNil(kept),
		}))
	} else {
		stdout("%s", written.MDPath)
	}
	return ExitOK
}

// secSince 返回从 t 到现在的秒数，精度对齐到毫秒。
// Node 版用 (Date.now()-t0)/1000，天然就是三位小数；统一成同样的粒度，
// 免得两份 timing 数据看起来不一致。
func secSince(t time.Time) float64 {
	return float64(time.Since(t).Milliseconds()) / 1000
}

func audioPathIn(workDir, format string) string {
	return filepath.Join(workDir, fmt.Sprintf("audio-%d.%s", time.Now().UnixNano(), media.AudioFormats[format].Ext))
}

func findDetected(list []asr.Detected, id string) *asr.Detected {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func mustJSON(v any) string {
	b, err := jsonout.MarshalIndent(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
