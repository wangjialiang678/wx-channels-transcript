package output

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		title, fallback, want string
	}{
		{"82亿美元，苏姿丰买下李飞飞的 WorldLabs。#AMD #AI", "id", "82亿美元-苏姿丰买下李飞飞的-WorldLabs-AMD-AI"},
		{"", "fallback", "fallback"},
		{"#？？？", "fallback", "fallback"},
		{"a/b\\c|d<e>f", "fb", "a-b-c-d-e-f"},
		{"  hello   world  ", "fb", "hello-world"},
	}
	for _, c := range cases {
		if got := Slugify(c.title, c.fallback, 32); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestSlugifyTruncates(t *testing.T) {
	title := "一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二三四五"
	got := Slugify(title, "fb", 32)
	if n := len([]rune(got)); n != 32 {
		t.Errorf("应截断到 32 个字符，得到 %d：%q", n, got)
	}
}

func TestFmtDuration(t *testing.T) {
	cases := map[float64]string{
		0: "未知", -1: "未知", 45: "45 秒", 433: "7 分 13 秒", 60: "1 分 0 秒",
	}
	for sec, want := range cases {
		if got := fmtDuration(sec); got != want {
			t.Errorf("fmtDuration(%v) = %q, want %q", sec, got, want)
		}
	}
}
