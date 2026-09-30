package cli

import (
	"os"
	"regexp"
	"strings"

	"github.com/wangjialiang678/wx-channels-transcript/internal/asr"
	"github.com/wangjialiang678/wx-channels-transcript/internal/env"
	"github.com/wangjialiang678/wx-channels-transcript/internal/jsonout"
	"github.com/wangjialiang678/wx-channels-transcript/internal/media"
)

const rule = "────────────────────────────────────────────────────"

// fmtSteps 把「额外步骤」渲染成人话。
func fmtSteps(steps []string) string {
	if len(steps) == 0 {
		return "零额外步骤"
	}
	return "需要：" + strings.Join(steps, " → ")
}

// cmdBackends 列出所有后端（含未配置的）与配置指引。
func cmdBackends(e map[string]string) int {
	detected := asr.DetectAll(e)
	stdout("本工具支持的 ASR 后端：\n")
	for _, d := range detected {
		mark := "❌"
		if d.Available {
			mark = "✅"
		}
		stdout("%s %-11s %s", mark, d.ID, d.Label)
		if d.Available {
			stdout("   已就绪：%s", d.Detail)
		} else {
			stdout("   未配置：%s", d.Reason)
		}
		stdout("   特性：直链直传=%s　直接吃mp4=%s　%s",
			yesNo(d.Backend.AcceptsURL()), yesNo(d.Backend.AcceptsMP4()), fmtSteps(d.ExtraSteps))
		stdout("   模型：%s", strings.Join(d.Backend.Models(), " / "))
		if d.Hint != "" {
			stdout("   如何配置：")
			for _, line := range strings.Split(d.Hint, "\n") {
				stdout("     %s", line)
			}
		}
		if d.Docs != "" {
			stdout("   官方文档：%s", d.Docs)
		}
		stdout("")
	}
	return ExitOK
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

var sentenceBreakRe = regexp.MustCompile(`。([^。])`)

// cmdScan 扫描本机能力并给出推荐。
func cmdScan(f *Flags, e map[string]string) int {
	ff := media.HasFfmpeg()
	detected := asr.DetectAll(e)
	chosen, ranked, reason := asr.Recommend(detected, ff)

	stdout("本机能力扫描\n%s", rule)
	stdout("ffmpeg：%s", func() string {
		if ff {
			return "✅ 已安装"
		}
		return "❌ 未安装"
	}())
	if !ff {
		stdout("     （只有「需要本地抽音频」的后端才用得上它；走阿里云时完全不需要）")
	}
	stdout("凭据文件：%s", func() string {
		files := env.ExistingFiles(nil)
		if len(files) == 0 {
			return "（无）"
		}
		home, _ := os.UserHomeDir()
		for i, p := range files {
			if home != "" {
				files[i] = strings.Replace(p, home, "~", 1)
			}
		}
		return strings.Join(files, "、")
	}())
	stdout("")

	for _, d := range detected {
		if d.Available {
			stdout("✅ %s", d.Label)
			stdout("     %s　%s", d.Detail, fmtSteps(d.ExtraSteps))
		} else {
			stdout("❌ %s", d.Label)
			stdout("     %s", d.Reason)
		}
	}
	stdout("")

	if chosen == nil {
		stdout("⚠️ 没有任何可用的 ASR 后端，无法转写。\n")
		stdout("最快的解决办法（任选其一）：")
		for _, d := range detected {
			if d.Available {
				continue
			}
			first := ""
			if d.Hint != "" {
				first = strings.SplitN(d.Hint, "\n", 2)[0]
			}
			stdout("  · %s：%s", d.Label, first)
			if d.Docs != "" {
				stdout("    文档：%s", d.Docs)
			}
		}
		return ExitNoBackend
	}

	stdout("%s", rule)
	stdout("推荐：%s", chosen.Label)
	stdout("")
	stdout("%s", sentenceBreakRe.ReplaceAllString(reason, "。\n$1"))
	stdout("")

	if f.Bool("json") {
		rankedOut := make([]map[string]any, 0, len(ranked))
		for _, r := range ranked {
			rankedOut = append(rankedOut, map[string]any{
				"id": r.ID, "label": r.Label, "score": r.Score, "notes": r.Notes,
			})
		}
		out, _ := jsonout.MarshalIndent(map[string]any{
			"ffmpeg": ff,
			"ranked": rankedOut,
			"chosen": chosen.ID,
			"reason": reason,
		})
		stdout("%s\n", out)
	}
	return ExitOK
}
