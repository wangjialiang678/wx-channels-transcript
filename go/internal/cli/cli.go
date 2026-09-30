// Package cli 实现命令行界面与主流程编排。
//
// 约定（脚本友好）：
//   - 进度信息一律走 stderr
//   - stdout 只输出最终的 .md 路径（或 --json 时的结构化结果）
package cli

import (
	"fmt"
	"os"

	"github.com/wangjialiang678/wx-channels-transcript/internal/env"
)

// Version 是工具版本号。与 Node 版保持同一个号，表示功能对齐。
const Version = "0.3.0"

// 退出码。与 Node 版的分法不同（Node 用的是 1..7 的流水线序号），
// 这里按「失败的性质」分类，更方便脚本判断。
const (
	ExitOK          = 0 // 成功
	ExitBadArgs     = 1 // 参数或环境问题
	ExitResolveFail = 2 // 解析 / 下载失败
	ExitAuth        = 3 // 登录态问题
	ExitNoBackend   = 4 // 未找到可用后端
	ExitTranscribe  = 5 // 转写失败
)

// stdout 只用于「最终结果」。
func stdout(format string, a ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", a...)
}

// stderr 用于一切进度与诊断信息。
func stderr(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

// Run 是程序入口，返回进程退出码。
func Run(argv []string) int {
	f := ParseArgs(argv)

	if f.Bool("help") || !f.Any() {
		stdout("%s", helpText)
		return ExitOK
	}
	if f.Bool("version") {
		stdout("%s", Version)
		return ExitOK
	}

	e := env.Load(nil)

	if len(f.Positional) > 0 {
		switch f.Positional[0] {
		case "backends":
			return cmdBackends(e)
		case "scan":
			return cmdScan(f, e)
		case "auth":
			return cmdAuth(f)
		}
	}

	if len(f.Positional) == 0 {
		stderr("错误：没有给出链接或文件。用 -h 看用法。")
		return ExitBadArgs
	}
	return cmdExtract(f, e)
}

const helpText = `sph-transcript v` + Version + ` —— 视频号链接 → 逐字稿

用法:
  sph scan                            扫描本机可用的 ASR 后端，并给出推荐
  sph <视频号链接>                     提取逐字稿
  sph backends                        列出所有支持的后端（含未配置的）
  sph auth                            查看登录态缓存
  sph auth --clear                    清除登录态缓存

输出选项:
  -o, --out <目录>        输出目录（默认 ./transcripts）
      --save-audio        保留音频文件到输出目录
      --save-video        保留原始视频到输出目录
      --audio-format <f>  音频格式：wav（默认）/ mp3 / m4a
      --keep              保留全部中间文件（等价于上面两个都开）

转写选项:
  -t, --to <后端>         指定 ASR 后端：bailian（默认）/ volcengine / local
  -m, --model <模型>      指定模型（各后端可用值见 sph backends）
  -l, --lang <语言>       语言提示，默认 zh

热词选项（提升专名识别率）:
      --hotwords <词表>   额外热词，逗号分隔，或给一个文件路径（每行一个）
      --no-auto-hotwords  关闭「从视频标题自动提取热词」（默认开启）
      --vocabulary-id <id> 阿里侧热词表 ID（需先在百炼控制台创建）
      --show-hotwords     只打印会自动提取的热词，不执行转写

登录态选项:
      --cookie <字符串>    手工提供元宝 cookie（完全跳过钥匙串授权）
      --browser <名称>     指定从哪个浏览器读：chrome / edge / brave / arc …
      --no-browser        禁止读取浏览器（只用缓存或 --cookie）

其它:
      --probe             只解析拿直链，不做转写
      --json              输出 JSON（便于 Agent 解析）
  -h, --help              显示帮助
  -v, --version           显示版本

说明:
  · 视频号解析需要元宝（yuanbao.tencent.com）的登录态，首次会弹一次
    macOS 钥匙串授权；之后会缓存复用，约一个月内不用再授权。
  · 不装证书、不改系统代理、不需要管理员权限。
  · **默认不产生任何中间文件**。走阿里云后端时连下载和 ffmpeg 都不需要。`
