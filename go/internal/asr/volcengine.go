package asr

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/wangjialiang678/wx-channels-transcript/internal/hotwords"
)

// ─────────────────────────────────────────────── 火山引擎（字节跳动 / 豆包语音）—— 两条路线
//
// ⚠️ 首先要分清两条**互不相通**的产品线（这是最容易踩的坑）：
//   - 「豆包语音」控制台 → 真 ASR。鉴权用 X-Api-Key（新版）或 APPID+Token（旧版）。
//   - 「火山方舟」平台   → 多模态 LLM 的「音频理解」。用 ark- 开头的 Key。
//
// 两者不通用：**ark- Key 调不了豆包语音的 ASR**。本文件只处理前者。
//
// 本文件实现两个后端：
//
//	volcengine（大模型，推荐）
//	  · 提交 /api/v3/auc/bigmodel/submit，查询 /query
//	  · volc.seedasr.auc = 模型 2.0（中文最好），volc.bigasr.auc = 1.0
//	  · 官方格式白名单 wav/mp3/ogg/spx/amr/aac/m4a —— **不含 mp4**，
//	    但实测（2026-09-30）直接提交 mp4 直链 + format:"mp4" 成功，
//	    服务端显然做了容器兼容（读的是内部音轨）。以实测为准。
//	  · 支持 audio.url 直接提交公网链接
//
//	volcengine-small（小模型）
//	  · 提交 /api/v1/auc/submit，查询 /query
//	  · 鉴权走 APPID+Token+cluster 老体系，不参与自动推荐
const (
	volcSubmitV3 = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/submit"
	volcQueryV3  = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/query"
	volcSubmitV1 = "https://openspeech.bytedance.com/api/v1/auc/submit"
	volcQueryV1  = "https://openspeech.bytedance.com/api/v1/auc/query"

	volcPollInterval = 2 * time.Second
	volcPollDeadline = 30 * time.Minute
)

// volcResource 是大模型线的资源 ID。
var volcResource = map[string]string{"2.0": "volc.seedasr.auc", "1.0": "volc.bigasr.auc"}

var (
	volcCommonEnvKeys = []string{"VOLC_ASR_API_KEY", "VOLC_SPEECH_API_KEY", "VOLC_TTS_API_KEY"}
	volcLegacyEnvKeys = []string{"VOLC_ASR_APP_ID", "VOLC_ASR_ACCESS_TOKEN", "VOLC_ASR_CLUSTER"}
)

const volcSetupHint = `在「豆包语音」控制台开通并取 Key（注意：不是火山方舟的 ark- Key）：
  1. 注册/登录火山引擎  https://console.volcengine.com/auth/signup
  2. 完成实名认证        https://console.volcengine.com/user/realname
  3. 进入新版豆包语音控制台并开通「录音文件识别大模型 - 标准版」
                        https://console.volcengine.com/speech/new
                        （有 20 小时/半年 免费额度，控制台点「试用」领取）
  4. 创建 API Key        https://console.volcengine.com/speech/new/setting/apikeys
  5. 写入环境变量        echo 'VOLC_ASR_API_KEY=你的key' >> ~/.claude/api-vault.env`

var audioExtRe = regexp.MustCompile(`(?i)\.(wav|mp3|ogg|m4a|spx|amr|aac)(\?|$)`)

// ─────────────────────────────────────────────── 大模型线（推荐）

type volcengine struct{}

func (*volcengine) ID() string           { return "volcengine" }
func (*volcengine) Label() string        { return "火山引擎·豆包语音（大模型标准版）" }
func (*volcengine) Models() []string     { return []string{"2.0", "1.0"} }
func (*volcengine) DefaultModel() string { return "2.0" }
func (*volcengine) AcceptsMP4() bool     { return true } // 实测通过，见文件头注释
func (*volcengine) AcceptsURL() bool     { return true }
func (*volcengine) NeedsFfmpeg() bool    { return false }

func (*volcengine) Detect(env map[string]string) Detection {
	for _, k := range volcCommonEnvKeys {
		if v := env[k]; len(v) > 10 {
			return Detection{
				Available: true,
				Detail:    "使用 " + k,
				Env:       map[string]string{"key": v, "mode": "new"},
			}
		}
	}
	appID, token, cluster := env["VOLC_ASR_APP_ID"], env["VOLC_ASR_ACCESS_TOKEN"], env["VOLC_ASR_CLUSTER"]
	if appID != "" && token != "" {
		return Detection{
			Available: true,
			Detail:    "使用 APP ID + Access Token（旧版控制台）",
			Env: map[string]string{
				"mode": "legacy", "appId": appID, "token": token, "cluster": cluster,
			},
		}
	}
	return Detection{
		Available: false,
		Reason:    "没有找到豆包语音的凭证",
		Hint:      volcSetupHint,
		Docs:      "https://www.volcengine.com/docs/6561/1354868",
	}
}

// Transcribe 提交并轮询火山大模型转写任务。
func (v *volcengine) Transcribe(ctx context.Context, req Request, progress func(string)) (*Result, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if req.URL == "" {
		return nil, fmt.Errorf("volcengine: 这条后端只接受公网 URL（audio.url）。\n" +
			"  本地文件请改用 -t bailian（它支持上传本地文件），或先把文件放到可公开访问的位置")
	}

	resourceID := volcResource["2.0"]
	if strings.Contains(req.Model, "1.0") {
		resourceID = volcResource["1.0"]
	}
	requestID := newUUID()

	// 直链通常是 mp4（视频号给的就是这个）。按扩展名推断，推不出来就当 mp4。
	fmtName := "mp4"
	if m := audioExtRe.FindStringSubmatch(req.URL); m != nil {
		fmtName = strings.ToLower(m[1])
	}

	request := map[string]any{
		"model_name":  "bigmodel",
		"enable_itn":  true,
		"enable_punc": true,
	}
	if req.Language != "" {
		request["language"] = req.Language
	}
	// 热词：corpus.context 直传（官方示例 "{\"hotwords\":[{\"word\":\"...\"}]}"）
	// 实测有效：喂入「苏姿丰」后，原本错成「朱志峰」的地方全部纠正。
	if len(req.Hotwords) > 0 {
		request["corpus"] = map[string]any{"context": hotwords.VolcengineContext(req.Hotwords)}
		shown := req.Hotwords
		suffix := ""
		if len(shown) > 8 {
			shown, suffix = shown[:8], "…"
		}
		progress(fmt.Sprintf("携带 %d 个热词：%s%s", len(req.Hotwords), strings.Join(shown, "、"), suffix))
	}

	body := map[string]any{
		"user":    map[string]any{"uid": "sph-transcript"},
		"audio":   map[string]any{"format": fmtName, "url": req.URL},
		"request": request,
	}

	progress("提交火山转写任务…")
	sub, err := v.do(ctx, volcSubmitV3, v.authHeaders(req, resourceID, requestID, true), body)
	if err != nil {
		return nil, err
	}
	if code := sub.statusCode; code != "20000000" {
		return nil, fmt.Errorf("volcengine: 提交失败 code=%s msg=%s\n"+
			"  常见原因：未开通该服务 / Key 不对（注意 ark- Key 不适用）/ 未实名",
			code, sub.headers.Get("X-Api-Message"))
	}

	progress(fmt.Sprintf("任务 %s 排队中…", requestID))
	out, err := v.poll(ctx, req, resourceID, requestID, progress)
	if err != nil {
		return nil, err
	}
	result := obj(out, "result")
	text := str(result, "text")
	if text == "" {
		return nil, fmt.Errorf("volcengine: 返回里没有文本（可能整段静音）")
	}
	var duration float64
	if ms, ok := num(obj(result, "audio_info"), "duration"); ok {
		duration = msToSeconds(ms)
	}
	if duration <= 0 {
		duration = lastSentenceEnd(arr(result, "utterances"))
	}
	modelName := "volcengine-" + resourceID[strings.LastIndex(resourceID, ".")+1:]
	return &Result{
		Text:        text,
		Model:       modelName,
		Sentences:   arr(result, "utterances"),
		Raw:         out,
		DurationSec: duration,
	}, nil
}

func (v *volcengine) authHeaders(req Request, resourceID, requestID string, submitting bool) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-Api-Resource-Id", resourceID)
	h.Set("X-Api-Request-Id", requestID)
	if submitting {
		h.Set("X-Api-Sequence", "-1")
	}
	if req.Env["mode"] == "legacy" {
		h.Set("X-Api-App-Key", req.Env["appId"])
		h.Set("X-Api-Access-Key", req.Env["token"])
	} else {
		h.Set("X-Api-Key", req.Env["key"])
	}
	return h
}

func (v *volcengine) poll(ctx context.Context, req Request, resourceID, requestID string, progress func(string)) (map[string]any, error) {
	deadline := time.Now().Add(volcPollDeadline)
	last := ""
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(volcPollInterval):
		}

		q, err := v.do(ctx, volcQueryV3, v.authHeaders(req, resourceID, requestID, false), map[string]any{})
		if err != nil {
			return nil, err
		}
		code := q.statusCode
		if code != last {
			progress("任务状态：" + code)
			last = code
		}
		switch code {
		case "20000000":
			return q.json, nil
		case "20000001", "20000002": // 处理中
			continue
		case "20000003":
			return nil, fmt.Errorf("volcengine: 音频是静音，未检测到人声")
		}
		if code != "" && !strings.HasPrefix(code, "20000") {
			return nil, fmt.Errorf("volcengine: 任务失败 code=%s msg=%s", code, q.headers.Get("X-Api-Message"))
		}
	}
	return nil, fmt.Errorf("volcengine: 轮询超时（30 分钟）")
}

type volcResponse struct {
	statusCode string
	headers    http.Header
	json       map[string]any
}

func (v *volcengine) do(ctx context.Context, url string, headers http.Header, body any) (volcResponse, error) {
	if body == nil {
		body = map[string]any{}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return volcResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return volcResponse{}, err
	}
	req.Header = headers

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return volcResponse{}, fmt.Errorf("volcengine: 请求失败：%w", err)
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(resp.Body)

	out := volcResponse{
		statusCode: resp.Header.Get("X-Api-Status-Code"),
		headers:    resp.Header,
	}
	if len(text) > 0 {
		var m map[string]any
		if err := json.Unmarshal(text, &m); err == nil {
			out.json = m
		}
	}
	return out, nil
}

// newUUID 生成一个 v4 UUID（避免为这点小事引第三方依赖）。
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 极端情况下退化成时间戳，仅用于请求去重，不用于安全用途
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// ─────────────────────────────────────────────── 小模型线（唯一官方写明支持 mp4）
//
// 官方原文格式：wav / ogg / mp3 / **mp4**。代价是识别质量是 1.0 之前的旧模型，
// 鉴权走 APPID+Token+cluster 老体系。它不参与自动推荐，只能显式 -t volcengine-small。
//
// 一手来源：https://www.volcengine.com/docs/DoubaoVoice/AudioFileRecognitionStandardEdition
type volcengineSmall struct{}

func (volcengineSmall) ID() string { return "volcengine-small" }
func (volcengineSmall) Label() string {
	return "火山引擎·录音文件识别（小模型，支持 mp4）"
}
func (volcengineSmall) Models() []string     { return []string{"standard"} }
func (volcengineSmall) DefaultModel() string { return "standard" }
func (volcengineSmall) AcceptsMP4() bool     { return true }
func (volcengineSmall) AcceptsURL() bool     { return true }
func (volcengineSmall) NeedsFfmpeg() bool    { return false }

func (volcengineSmall) Detect(env map[string]string) Detection {
	appID, token, cluster := env["VOLC_ASR_APP_ID"], env["VOLC_ASR_ACCESS_TOKEN"], env["VOLC_ASR_CLUSTER"]
	// 官方文档里 app.appid / app.token / app.cluster 三个字段全部标了「必填」，
	// 少任何一个服务端都会拒，所以这里三个都要有才算可用。
	if appID != "" && token != "" && cluster != "" {
		return Detection{
			Available: true,
			Detail:    "APP ID + Token + Cluster（老体系鉴权）",
			Env: map[string]string{
				"mode": "legacy", "appId": appID, "token": token, "cluster": cluster,
			},
		}
	}
	var missing []string
	if appID == "" {
		missing = append(missing, "VOLC_ASR_APP_ID")
	}
	if token == "" {
		missing = append(missing, "VOLC_ASR_ACCESS_TOKEN")
	}
	if cluster == "" {
		missing = append(missing, "VOLC_ASR_CLUSTER")
	}
	return Detection{
		Available: false,
		Reason:    "小模型线需要三个字段（老体系），缺：" + strings.Join(missing, "、"),
		Hint: `小模型的三个凭证**不在**新版 API Key 页面，而在控制台的「创建应用 / 开通服务」入口：
  1. 进入豆包语音控制台  https://console.volcengine.com/speech/new
  2. 创建应用并开通「录音文件识别（标准版）」
  3. 在应用详情里复制 APP ID、Access Token、Cluster ID 三个字段
  4. 写入环境变量：
       echo 'VOLC_ASR_APP_ID=...'       >> ~/.claude/api-vault.env
       echo 'VOLC_ASR_ACCESS_TOKEN=...' >> ~/.claude/api-vault.env
       echo 'VOLC_ASR_CLUSTER=...'      >> ~/.claude/api-vault.env

  ⚠️ 注意：新版控制台的 VOLC_ASR_API_KEY（X-Api-Key）**只能调大模型接口**，
     不能用于小模型；两者是两套互不通用的鉴权体系。`,
		Docs: "https://www.volcengine.com/docs/DoubaoVoice/AudioFileRecognitionStandardEdition",
	}
}

func (volcengineSmall) Transcribe(ctx context.Context, req Request, progress func(string)) (*Result, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if req.URL == "" {
		return nil, fmt.Errorf("volcengine-small: 需要公网 URL（audio.url）")
	}
	appID, token, cluster := req.Env["appId"], req.Env["token"], req.Env["cluster"]
	if cluster == "" {
		cluster = "volcengine_input_common"
	}

	body := map[string]any{
		"app":       map[string]any{"appid": appID, "token": token, "cluster": cluster},
		"user":      map[string]any{"uid": "sph-transcript"},
		"audio":     map[string]any{"format": "mp4", "url": req.URL},
		"additions": map[string]any{"use_itn": "True", "with_speaker_info": "False"},
		"request":   map[string]any{"model_name": "bigmodel", "enable_itn": true, "enable_punc": true},
	}

	progress("提交火山小模型转写任务…")
	sj, err := postJSON(ctx, volcSubmitV1, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer; " + token,
	}, body)
	if err != nil {
		return nil, fmt.Errorf("volcengine-small: %w", err)
	}
	taskID := nonEmpty(str(obj(sj, "resp"), "id"), str(sj, "id"))
	subCode, hasSubCode := num(sj, "code")
	if !hasSubCode {
		if c, ok := num(obj(sj, "resp"), "code"); ok {
			subCode, hasSubCode = c, true
		}
	}
	if taskID == "" || (hasSubCode && subCode != 1000) {
		return nil, fmt.Errorf("volcengine-small: 提交失败 %s\n"+
			"  常见原因：APP ID / Token / cluster 任一不对，或该模型未开通", truncateJSONRaw(sj, 300))
	}

	progress(fmt.Sprintf("任务 %s 处理中…", taskID))
	deadline := time.Now().Add(volcPollDeadline)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
		qj, err := postJSON(ctx, volcQueryV1, map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer; " + token,
		}, map[string]any{"appid": appID, "token": token, "cluster": cluster, "id": taskID})
		if err != nil {
			return nil, fmt.Errorf("volcengine-small: %w", err)
		}
		code, ok := num(qj, "code")
		if !ok {
			if c, ok2 := num(obj(qj, "resp"), "code"); ok2 {
				code, ok = c, true
			}
		}
		switch {
		case code == 1000:
			return parseSmallResult(qj, progress)
		case code == 1001 || code == 1002:
			continue // 处理中
		case code == 1003:
			return nil, fmt.Errorf("volcengine-small: 访问超频（超出并发配额），稍后重试")
		case ok:
			return nil, fmt.Errorf("volcengine-small: 任务失败 code=%v msg=%s", code, str(qj, "message"))
		}
	}
	return nil, fmt.Errorf("volcengine-small: 轮询超时（30 分钟）")
}

// parseSmallResult 老接口的结果没有公开稳定 schema，做宽松解析。
func parseSmallResult(qj map[string]any, progress func(string)) (*Result, error) {
	utt := arr(obj(qj, "resp"), "utterances")
	if len(utt) == 0 {
		utt = arr(qj, "utterances")
	}
	text := nonEmpty(str(obj(qj, "resp"), "text"), str(qj, "text"))
	if text == "" {
		var parts []string
		for _, u := range utt {
			if s := str(u, "text"); s != "" {
				parts = append(parts, s)
			}
		}
		text = strings.Join(parts, "")
	}
	if text == "" {
		progress("提示：结果结构可能已变化，原始返回已写入 raw 字段")
		return nil, fmt.Errorf("volcengine-small: 返回里没有文本 %s", truncateJSONRaw(qj, 300))
	}
	return &Result{Text: text, Model: "volcengine-small", Sentences: utt, Raw: qj}, nil
}

func postJSON(ctx context.Context, url string, headers map[string]string, body any) (map[string]any, error) {
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
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(text, &m); err != nil {
		return nil, fmt.Errorf("非 JSON 响应 %s", firstNStr(string(text), 200))
	}
	return m, nil
}
