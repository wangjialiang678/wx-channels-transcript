// Package resolve 把视频号分享链接换成可下载的媒体直链。
//
// 三步走（每一步都在真实会话上验证过）：
//
//  1. POST /api/weixin/get_parse_result   分享链接 → wx_export_id
//  2. POST /api/findergetobjecturl        wx_export_id → 直链
//  3. 直链自带 token+sign 签名，下载时不需要任何 cookie，也不需要解密
//
// 注意：这是元宝（腾讯自家的 AI 助手）对外暴露的接口，属于**非公开接口**。
// 腾讯随时可能调整字段或加风控，代码里对字段名做了多重兜底，
// 但仍然要预期它会失效。
package resolve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	baseURL   = "https://yuanbao.tencent.com"
	parseURL  = baseURL + "/api/weixin/get_parse_result"
	objectURL = baseURL + "/api/findergetobjecturl"

	requestTimeout = 15 * time.Second
	userAgent      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
)

// Info 是一条视频号的解析结果。
type Info struct {
	ShareID   string `json:"shareId"`
	ExportID  string `json:"exportId"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	MediaURL  string `json:"mediaUrl"`
	URL       string `json:"url"`
	IsLocal   bool   `json:"isLocal,omitempty"`
	LocalPath string `json:"localPath,omitempty"`

	// DurationSec 由 ASR 结果回填，不是解析阶段拿到的。
	DurationSec float64 `json:"-"`
}

// AuthError 表示失败原因是登录态，上层据此决定「要不要重新授权」。
type AuthError struct{ Msg string }

func (e *AuthError) Error() string { return e.Msg }

var (
	shareIDPatterns = []*regexp.Regexp{
		regexp.MustCompile(`weixin\.qq\.com/sph/([A-Za-z0-9_-]+)`),
		regexp.MustCompile(`channels\.weixin\.qq\.com/[^?#]*\?(?:[^#]*&)?id=([A-Za-z0-9_-]+)`),
	}
	bareShareIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,}$`)
)

// ParseShareID 从已知的两种分享链接形态里取出 shareId；
// 用户直接粘贴裸 shareId 也认。取不到时返回空串。
func ParseShareID(url string) string {
	s := strings.TrimSpace(url)
	for _, re := range shareIDPatterns {
		if m := re.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	if bareShareIDPattern.MatchString(s) {
		return s
	}
	return ""
}

// pick 从多个候选字段名里取第一个非空字符串——接口字段改名时不至于直接挂掉。
func pick(obj map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := obj[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// unwrap 拆掉常见的 { data: {...} } 信封。
func unwrap(json map[string]any) map[string]any {
	if d, ok := json["data"].(map[string]any); ok {
		return d
	}
	if json == nil {
		return map[string]any{}
	}
	return json
}

type apiResponse struct {
	httpStatus int
	json       map[string]any
}

func callAPI(ctx context.Context, url, cookie string, body any) (apiResponse, error) {
	var reader io.Reader
	method := http.MethodGet
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(b)
		method = http.MethodPost
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return apiResponse{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Source", "web")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", cookie)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return apiResponse{}, fmt.Errorf("请求元宝失败：%w", err)
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		return apiResponse{}, fmt.Errorf("读取元宝响应失败：%w", err)
	}

	out := apiResponse{httpStatus: resp.StatusCode}
	if len(text) > 0 {
		var m map[string]any
		if err := json.Unmarshal(text, &m); err == nil {
			out.json = m
		}
	}
	return out, nil
}

var (
	authMsgRe        = regexp.MustCompile(`(?i)(get\s*token\s*err|登录|未登录|unauthorized|invalid\s*session|请先登录|重新登录)`)
	unsupportedMsgRe = regexp.MustCompile(`(?i)(直播|live\b|回放|replay|不支持|unsupported|已下架|已删除|removed|deleted|违规)`)
	notFoundMsgRe    = regexp.MustCompile(`(?i)(不存在|not\s*found|无效|已过期|失效|404)`)
)

// classify 把业务错误归类，便于上层给出不同的重试建议。
func classify(httpStatus int, json map[string]any) string {
	if httpStatus == http.StatusUnauthorized || httpStatus == http.StatusForbidden {
		return "auth"
	}
	if httpStatus == http.StatusNotFound {
		return "not_found"
	}
	var msg string
	if e, ok := json["error"].(map[string]any); ok {
		msg = pick(e, "message")
	}
	if msg == "" {
		msg = pick(json, "message", "msg")
	}
	msg = strings.TrimSpace(msg)
	if strings.EqualFold(msg, "success") || msg == "" {
		return ""
	}
	switch {
	case authMsgRe.MatchString(msg):
		return "auth"
	case unsupportedMsgRe.MatchString(msg):
		return "unsupported"
	case notFoundMsgRe.MatchString(msg):
		return "not_found"
	}
	return ""
}

var imageExtRe = regexp.MustCompile(`(?i)\.(jpe?g|png|gif|webp|bmp|svg|ico)(\?|$)`)
var videoExtRe = regexp.MustCompile(`(?i)\.(mp4|m3u8|ts|mov|m4v|flv)(\?|$)`)
var mediaHostRe = regexp.MustCompile(`(?i)(finder\.video\.qq\.com|\.video\.qq\.com|\.tc\.qq\.com|mmfinder)`)

func looksLikeMediaURL(s string) bool {
	if !strings.HasPrefix(strings.ToLower(s), "http://") && !strings.HasPrefix(strings.ToLower(s), "https://") {
		return false
	}
	if imageExtRe.MatchString(s) {
		return false
	}
	// finder.video.qq.com/.../stodownload?... 没有扩展名，所以还要按主机名判断
	return videoExtRe.MatchString(s) || mediaHostRe.MatchString(s)
}

func truncateJSON(m map[string]any, n int) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "<无法序列化>"
	}
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}

// Resolve 解析一条视频号链接。
func Resolve(ctx context.Context, url, cookie string) (Info, error) {
	shareID := ParseShareID(url)
	if shareID == "" {
		return Info{}, fmt.Errorf("这不是可识别的视频号链接：%s\n"+
			"  支持的形态：https://weixin.qq.com/sph/xxxx\n"+
			"               https://channels.weixin.qq.com/finder-preview/pages/sph?id=xxxx", url)
	}

	// 步骤 1：分享链接 → export id
	p, err := callAPI(ctx, parseURL, cookie, map[string]any{
		"type": "video_channel_url", "url": url, "scene": 1,
	})
	if err != nil {
		return Info{}, err
	}
	switch classify(p.httpStatus, p.json) {
	case "auth":
		return Info{}, &AuthError{"元宝登录态在解析过程中被拒绝"}
	case "unsupported":
		return Info{}, fmt.Errorf("该链接不受支持（直播 / 回放 / 已下架等）")
	case "not_found":
		return Info{}, fmt.Errorf("视频不存在或已失效")
	}

	d := unwrap(p.json)
	exportID := pick(d, "wx_export_id", "exportId", "export_id")
	if exportID == "" {
		return Info{}, fmt.Errorf("拿不到 export_id —— 该视频可能已删除、私密或过期。\n"+
			"  服务端返回：%s", truncateJSON(p.json, 300))
	}
	title := pick(d, "desc", "title", "objectDesc")
	author := pick(d, "author", "nickname", "authorName")

	// 步骤 2：export id → 直链
	// ⚠️ exportId 必须是「字符串」；传数组会返回业务码 500。
	o, err := callAPI(ctx, objectURL, cookie, map[string]any{"exportId": exportID})
	if err != nil {
		return Info{}, err
	}
	if classify(o.httpStatus, o.json) == "auth" {
		return Info{}, &AuthError{"元宝登录态在换取直链时被拒绝"}
	}
	od := unwrap(o.json)
	mediaURL := pick(o.json, "videoUrl", "video_url", "url")
	if mediaURL == "" {
		mediaURL = pick(od, "videoUrl", "video_url", "url")
	}
	if mediaURL == "" || !looksLikeMediaURL(mediaURL) {
		return Info{}, fmt.Errorf("拿到了 export_id，但换不到媒体直链。\n"+
			"  服务端返回：%s", truncateJSON(o.json, 300))
	}

	return Info{
		ShareID:  shareID,
		ExportID: exportID,
		Title:    title,
		Author:   author,
		MediaURL: mediaURL,
		URL:      url,
	}, nil
}
