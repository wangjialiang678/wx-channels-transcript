package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ─────────────────────────────────────────────── 阿里云百炼（DashScope）录音文件识别
//
// 为什么把它排在推荐第一位：
//   - **能直接吃 mp4** —— 于是整条链路可以「拿到直链 → 提交 → 出稿」，
//     既不用下载到本地，也不用 ffmpeg 抽音频
//   - 支持公网 URL 直传，和视频号直链天然契合
//   - 中文识别质量好，专名比通用模型准
//
// 接口形态：异步提交 + 轮询（同步接口有 5 分钟 / 10MB 上限）。
// ⚠️ 两个模型的请求体结构不一样，别混：
//
//	qwen3-asr-flash-filetrans  → input.file_url（单对象），结果 output.result.transcription_url
//	paraformer-v2 / fun-asr 等 → input.file_urls（数组），结果 output.results[].transcription_url
const (
	bailianEndpoint     = "https://dashscope.aliyuncs.com/api/v1/services/audio/asr/transcription"
	bailianTaskURL      = "https://dashscope.aliyuncs.com/api/v1/tasks"
	bailianUploadPolicy = "https://dashscope.aliyuncs.com/api/v1/uploads"

	bailianDefaultModel = "qwen3-asr-flash-filetrans"
	bailianPollInterval = 2 * time.Second
	bailianPollDeadline = 30 * time.Minute
)

// 单对象结构的模型（用 file_url 而非 file_urls）。
var bailianSingleObject = regexp.MustCompile(`^qwen3-asr-flash-filetrans$`)

// 上传到 OSS 时文件名里不能出现的字符。
var unsafeFilenameRe = regexp.MustCompile(`[^\w.-]`)

type bailian struct{}

func (*bailian) ID() string    { return "bailian" }
func (*bailian) Label() string { return "阿里云百炼（DashScope）" }

// 只保留大模型 ASR。paraformer-v2 已移除：实测（同一段 433s 中文口播，
// 14 个专名判据点）它只有 10.5% 专名准确率，而 qwen3 是 92.9%。省钱不值得。
func (*bailian) Models() []string     { return []string{"qwen3-asr-flash-filetrans", "fun-asr"} }
func (*bailian) DefaultModel() string { return bailianDefaultModel }
func (*bailian) AcceptsMP4() bool     { return true } // 实测：23MB mp4 直传成功
func (*bailian) AcceptsURL() bool     { return true }
func (*bailian) NeedsFfmpeg() bool    { return false }

var bailianEnvKeys = []string{"DASHSCOPE_API_KEY", "DASHSCOPE_API_KEY_ASR"}

func (*bailian) Detect(env map[string]string) Detection {
	for _, k := range bailianEnvKeys {
		if v := env[k]; len(v) > 10 {
			return Detection{
				Available: true,
				Detail:    "使用 " + k,
				Env:       map[string]string{"key": v, "keyName": k},
			}
		}
	}
	return Detection{
		Available: false,
		Reason:    "没有找到 DASHSCOPE_API_KEY",
		Hint: "在 https://bailian.console.aliyun.com/ 开通百炼并创建 API Key，" +
			"然后写入 ~/.claude/api-vault.env 或导出为环境变量。",
		Docs: "https://help.aliyun.com/zh/model-studio/getting-started/",
	}
}

// Transcribe 提交并等待结果。url 与 file 二选一（url 优先）。
func (b *bailian) Transcribe(ctx context.Context, req Request, progress func(string)) (*Result, error) {
	if progress == nil {
		progress = func(string) {}
	}
	key := req.Env["key"]
	if key == "" {
		return nil, fmt.Errorf("bailian: 缺少 DASHSCOPE_API_KEY")
	}
	useModel := req.Model
	if useModel == "" {
		useModel = bailianDefaultModel
	}

	fileURL := req.URL
	if fileURL == "" {
		if req.File == "" {
			return nil, fmt.Errorf("bailian: 需要提供 url 或 file")
		}
		progress("上传音频到百炼临时存储…")
		u, err := b.upload(ctx, key, req.File, useModel, progress)
		if err != nil {
			return nil, err
		}
		fileURL = u
	}

	single := bailianSingleObject.MatchString(useModel)

	params := map[string]any{"channel_id": []int{0}}
	if req.Language != "" {
		params["language_hints"] = []string{req.Language}
	}

	// 热词：异步接口（filetrans）走 parameters.corpus.text。
	//
	// ⚠️ 这里踩过两个坑，都实测过：
	//  1. `input.messages`（同步版 qwen3-asr-flash 的写法）**在这里直接报错**：
	//     InvalidParameter.MalformedURL / "A valid file URL is required."
	//  2. `parameters.context` 任务会 SUCCEEDED，但**热词完全无效**（输出与无热词逐字相同）。
	//     这就是「未知参数被静默忽略」的陷阱——判断参数是否生效不能看任务成功与否。
	switch {
	case req.CorpusText != "":
		params["corpus"] = map[string]any{"text": req.CorpusText}
		progress(fmt.Sprintf("携带热词上下文（%d 字符）", len([]rune(req.CorpusText))))
	case len(req.Hotwords) > 0:
		params["corpus"] = map[string]any{"text": strings.Join(req.Hotwords, ", ")}
		progress(fmt.Sprintf("携带 %d 个热词", len(req.Hotwords)))
	}

	// vocabulary_id 仍保留支持（预编译词表），但优先级低于 corpus
	if req.VocabularyID != "" {
		if _, hasCorpus := params["corpus"]; !hasCorpus {
			params["vocabulary_id"] = req.VocabularyID
			progress("使用预编译热词表 " + req.VocabularyID)
		}
	}

	input := map[string]any{"file_url": fileURL}
	if !single {
		input = map[string]any{"file_urls": []string{fileURL}}
	}
	body := map[string]any{
		"model":      useModel,
		"input":      input,
		"parameters": params,
	}

	progress(fmt.Sprintf("提交转写任务（%s）…", useModel))
	submit, err := b.post(ctx, bailianEndpoint, key, body, strings.HasPrefix(fileURL, "oss://"))
	if err != nil {
		return nil, err
	}
	taskID := str(obj(submit, "output"), "task_id")
	if taskID == "" {
		return nil, fmt.Errorf("bailian: 提交失败 %s", truncateJSONRaw(submit, 300))
	}

	progress(fmt.Sprintf("任务 %s 排队中…", taskID))
	// poll 返回完整的任务响应：output 里有结果地址，顶层的 usage.seconds 是音频时长。
	task, err := b.poll(ctx, key, taskID, progress)
	if err != nil {
		return nil, err
	}
	out := obj(task, "output")

	var transcriptionURL string
	if single {
		transcriptionURL = str(obj(out, "result"), "transcription_url")
	} else {
		if results := arr(out, "results"); len(results) > 0 {
			transcriptionURL = str(results[0], "transcription_url")
		}
	}
	if transcriptionURL == "" {
		return nil, fmt.Errorf("bailian: 任务完成但拿不到结果地址 %s", truncateJSONRaw(out, 300))
	}

	// 结果 JSON 24 小时后失效，必须立刻取回
	data, err := b.fetchJSON(ctx, transcriptionURL, "")
	if err != nil {
		return nil, fmt.Errorf("bailian: 下载结果失败：%w", err)
	}

	var texts []string
	var sentences []any
	for _, t := range arr(data, "transcripts") {
		if s := str(t, "text"); s != "" {
			texts = append(texts, s)
		}
		if ss := arr(t, "sentences"); len(ss) > 0 {
			sentences = append(sentences, ss...)
		}
	}
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	if text == "" {
		return nil, fmt.Errorf("bailian: 结果里没有文本（可能整段静音）%s", truncateJSONRaw(data, 200))
	}

	// 时长优先取任务响应的 usage.seconds（单位就是秒）；
	// 拿不到时退而用最后一句的 end_time（毫秒）当近似值。
	var duration float64
	if sec, ok := num(obj(task, "usage"), "seconds"); ok {
		duration = sec
	}
	if duration <= 0 {
		duration = lastSentenceEnd(sentences)
	}

	return &Result{Text: text, Model: useModel, Sentences: sentences, Raw: data, DurationSec: duration}, nil
}

// post 提交一个异步任务。
func (b *bailian) post(ctx context.Context, url, key string, body any, needsOssResolve bool) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DashScope-Async", "enable")
	// ⚠️ 当 file_url 是 oss:// 开头时（本地文件走临时上传后的形态），
	// 这个头是**必需**的。漏了会报 FILE_DOWNLOAD_FAILED，
	// 而报错信息完全不提是缺这个头——极难排查。
	if needsOssResolve {
		req.Header.Set("X-DashScope-OssResourceResolve", "enable")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bailian: 请求失败：%w", err)
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(resp.Body)

	var m map[string]any
	if err := json.Unmarshal(text, &m); err != nil {
		return nil, fmt.Errorf("bailian: 非 JSON 响应 %s", firstNStr(string(text), 200))
	}
	return m, nil
}

// poll 轮询任务直到结束，返回**完整的任务响应**。
func (b *bailian) poll(ctx context.Context, key, taskID string, progress func(string)) (map[string]any, error) {
	deadline := time.Now().Add(bailianPollDeadline)
	last := ""
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(bailianPollInterval):
		}

		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, bailianTaskURL+"/"+taskID, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("bailian: 轮询失败：%w", err)
		}
		text, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()

		var j map[string]any
		if err := json.Unmarshal(text, &j); err != nil {
			return nil, fmt.Errorf("bailian: 轮询响应非 JSON %s", firstNStr(string(text), 200))
		}
		output := obj(j, "output")
		st := str(output, "task_status")
		if st != last {
			progress("任务状态：" + st)
			last = st
		}
		switch st {
		case "SUCCEEDED":
			return j, nil
		case "FAILED", "UNKNOWN":
			return nil, fmt.Errorf("bailian: 任务失败 %s", truncateJSONRaw(output, 300))
		}
	}
	return nil, fmt.Errorf("bailian: 轮询超时（30 分钟）")
}

// upload 上传本地文件到百炼临时存储，拿 oss:// 地址。
// 用官方「临时上传」通道，不需要用户自备 OSS。
func (b *bailian) upload(ctx context.Context, key, filePath, model string, progress func(string)) (string, error) {
	url := bailianUploadPolicy + "?action=getPolicy&model=" + model
	policy, err := b.fetchJSON(ctx, url, key)
	if err != nil {
		return "", fmt.Errorf("bailian: 取上传凭证失败：%w", err)
	}
	d := obj(policy, "data")
	if str(d, "upload_host") == "" {
		return "", fmt.Errorf("bailian: 取上传凭证失败 %s", truncateJSONRaw(policy, 200))
	}

	// 文件名必须净化：含中文/空格时服务端会报 InvalidFile.DownloadFailed
	safeName := unsafeFilenameRe.ReplaceAllString(filepath.Base(filePath), "_")
	objectKey := str(d, "upload_dir") + "/" + safeName

	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fields := [][2]string{
		{"OSSAccessKeyId", str(d, "oss_access_key_id")},
		{"Signature", str(d, "signature")},
		{"policy", str(d, "policy")},
		{"x-oss-object-acl", str(d, "x_oss_object_acl")},
		{"x-oss-forbid-overwrite", str(d, "x_oss_forbid_overwrite")},
		{"key", objectKey},
		{"success_action_status", "200"},
	}
	for _, kv := range fields {
		if err := mw.WriteField(kv[0], kv[1]); err != nil {
			return "", err
		}
	}
	fw, err := mw.CreateFormFile("file", safeName)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, str(d, "upload_host"), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bailian: 上传失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("bailian: 上传失败 HTTP %d %s", resp.StatusCode, firstNStr(string(body), 200))
	}
	return "oss://" + objectKey, nil
}

func (b *bailian) fetchJSON(ctx context.Context, url, key string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(text, &m); err != nil {
		return nil, fmt.Errorf("非 JSON 响应 %s", firstNStr(string(text), 200))
	}
	return m, nil
}
