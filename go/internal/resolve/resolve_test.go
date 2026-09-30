package resolve

import "testing"

func TestParseShareID(t *testing.T) {
	cases := map[string]string{
		// 两种已验证的分享链接形态
		"https://weixin.qq.com/sph/AwBLqB239x":                                    "AwBLqB239x",
		"https://channels.weixin.qq.com/finder-preview/pages/sph?id=AbC123_-x":    "AbC123_-x",
		"https://channels.weixin.qq.com/finder-preview/pages/sph?foo=1&id=AbCdEf": "AbCdEf",
		"  https://weixin.qq.com/sph/AwBLqB239x  ":                                "AwBLqB239x",
		// 用户直接粘裸 shareId 也认
		"AwBLqB239x": "AwBLqB239x",
		// 不认识的形态
		"https://example.com/foo": "",
		"abc":                     "",
		"":                        "",
	}
	for in, want := range cases {
		if got := ParseShareID(in); got != want {
			t.Errorf("ParseShareID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLooksLikeMediaURL(t *testing.T) {
	yes := []string{
		"https://finder.video.qq.com/251/20302/stodownload?encfilekey=xx&token=yy",
		"https://x.video.qq.com/a.mp4",
		"https://a.tc.qq.com/x",
		"https://example.com/a.m3u8",
	}
	no := []string{
		"",
		"ftp://x",
		"https://finder.video.qq.com/cover.jpg",
		"https://example.com/page.html",
	}
	for _, u := range yes {
		if !looksLikeMediaURL(u) {
			t.Errorf("应判定为媒体直链：%s", u)
		}
	}
	for _, u := range no {
		if looksLikeMediaURL(u) {
			t.Errorf("不该判定为媒体直链：%s", u)
		}
	}
}

func TestPickFallsBackAcrossFieldNames(t *testing.T) {
	// 接口字段改名时的兜底：wx_export_id / exportId / export_id 都要能认
	m := map[string]any{"exportId": "E1"}
	if got := pick(m, "wx_export_id", "exportId", "export_id"); got != "E1" {
		t.Errorf("pick 兜底失败：%q", got)
	}
	if got := pick(map[string]any{}, "a", "b"); got != "" {
		t.Errorf("全空时应返回空串，得到 %q", got)
	}
}
