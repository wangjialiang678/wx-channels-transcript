// Command sph 是「视频号链接 → 逐字稿」的命令行工具。
//
// 用法概览：
//
//	sph <视频号链接或本地文件> [-o 输出目录] [选项]
//	sph scan       扫描本机可用的 ASR 后端并给出推荐
//	sph backends   列出所有后端（含未配置的）
//	sph auth       查看 / 清除登录态缓存
//
// 进度信息一律走 stderr，stdout 只输出最终的 .md 路径，便于脚本取用。
package main

import (
	"os"

	"github.com/wangjialiang678/wx-channels-transcript/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
