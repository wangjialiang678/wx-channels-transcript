package hotwords

import (
	"strings"
	"testing"
)

// 用真实视频标题做回归：这几个专名正是 ASR 最容易写错的那批。
const realTitle = "82亿美元，苏姿丰买下李飞飞的 WolrdLabs。AI的下一场战争，在整个物理世。" +
	"#李飞飞 #苏姿丰 #AMD #人工智能 #AI #WorldLabs #科技前沿"

func TestExtractFromRealTitle(t *testing.T) {
	got := Extract(realTitle, nil)
	want := []string{"李飞飞", "苏姿丰", "AMD", "WorldLabs", "WolrdLabs"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Extract() = %v, want %v", got, want)
	}
}

func TestExtractFiltersStopwordsAndShortWords(t *testing.T) {
	got := Extract("#AI #人工智能 #科技前沿 #A #OK", nil)
	if strings.Join(got, "|") != "OK" {
		t.Errorf("停用词过滤不对：%v", got)
	}
}

func TestExtractExtra(t *testing.T) {
	got := Extract("标题", []string{"Qwen3", "  ", "Qwen3"})
	if strings.Join(got, "|") != "Qwen3" {
		t.Errorf("额外词未去重/未过滤空值：%v", got)
	}
}

func TestParseArg(t *testing.T) {
	cases := map[string][]string{
		"a,b":   {"a", "b"},
		"a，b c": {"a", "b", "c"},
		"":      nil,
		"  a  ": {"a"},
		"a、b":   {"a、b"}, // '、' 不是分隔符，与 Node 版一致
	}
	for in, want := range cases {
		got := ParseArg(in)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("ParseArg(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildCorpusText(t *testing.T) {
	if got := BuildCorpusText(nil); got != "" {
		t.Errorf("空词表应返回空串，得到 %q", got)
	}
	got := BuildCorpusText([]string{"苏姿丰", "李飞飞"})
	if !strings.HasPrefix(got, "本次录音涉及以下专有名词") || !strings.HasSuffix(got, "苏姿丰、李飞飞") {
		t.Errorf("corpus.text 格式不符：%q", got)
	}
}

func TestVolcengineContext(t *testing.T) {
	got := VolcengineContext([]string{"苏姿丰"})
	want := `{"hotwords":[{"word":"苏姿丰"}]}`
	if got != want {
		t.Errorf("VolcengineContext() = %q, want %q", got, want)
	}
	if VolcengineContext(nil) != "" {
		t.Error("空词表应返回空串")
	}
}

func TestUniqueKeepsOrder(t *testing.T) {
	got := Unique([]string{"b", "a", "b", "", "c"})
	if strings.Join(got, "|") != "b|a|c" {
		t.Errorf("Unique() = %v", got)
	}
}
