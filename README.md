# wx-channels-transcript

**微信视频号 (Weixin Channels) → 逐字稿。**

一条命令，把视频号链接变成可编辑的文字稿。不需要装证书、不改系统代理、不用管理员权限。

```bash
wx-channels-transcript "https://weixin.qq.com/sph/xxxx"
```

> **如果你或你的 Agent 要直接执行**：先读 [`skill/SKILL.md`](skill/SKILL.md)——那里有完整的决策顺序、前置检查、失败处置和硬约束。

---

## 两种实现，按需选

仓库里有两份独立实现，功能等价（对同一链接产出的正文逐字节相同）：

| | **Go 版**（[`go/`](go/)） | **Node 版**（[`node/`](node/)） |
|---|---|---|
| **用户需要装什么** | **什么都不用装**（单二进制） | Node ≥ 22 |
| 分发方式 | 发一个约 9MB 的文件 | 需要自带 Node 环境 |
| 代码量 | 约 4200 行 | 约 2000 行 |
| 第三方依赖 | 1 个（SQLite 驱动，仅编译时需要） | **零**（全用 Node 内置能力） |
| 适合 | **给朋友用** | 自己读、改、二次开发 |

选 Go 版：你想把工具发给不懂技术的人，对方双击就能跑。
选 Node 版：你本机有 Node，想快速看懂/修改实现。

## 它解决什么问题

想扒一条视频号的文案，通常的做法是：手机上转发给元宝 → 等它生成 → 再复制粘贴出来。一两条还行，做对标拆解时要几十条，就成了灾难。

这个工具把整条链路压成一条命令：

```
视频号链接 ──解析──▶ 可下载直链 ──转写──▶ 逐字稿
```

**中间什么都不留**——不下载视频、不抽音频、不落临时文件。

## 为什么不需要装证书、改代理

同类工具多走 **MITM 抓包**路线（[res-downloader](https://github.com/putyy/res-downloader)、[wx_channels_download](https://github.com/ltaoo/wx_channels_download)），要**以管理员身份运行、安装根证书、启动本地代理、再在微信里播放一遍**。

本工具走另一条路：用元宝（腾讯自家的 AI 助手）的解析接口把分享链接换成直链，然后直接下载。

**代价**：依赖腾讯的非公开接口，且需要元宝登录态。

## 开始前你需要准备什么

| 需要 | 说明 |
|---|---|
| **一个云端 ASR 的 Key** | 把音频转成文字需要一个语音识别服务。**阿里云百炼**是最省事的一条（有免费额度，超出按量付费）。配置方法见 [`docs/backends.md`](docs/backends.md)。 |
| **浏览器登录过元宝** | 用你日常用的浏览器打开 https://yuanbao.tencent.com 扫码登录一次即可。 |
| （Node 版）**Node ≥ 22** | 用到内置的 `fetch` / `node:sqlite` / `node:crypto`。除此之外**零依赖**。 |
| **macOS + Chrome 系浏览器** | ⚠️ **目前只实现了在 macOS 上读取 Chrome/Edge/Brave 等浏览器的登录态**。Windows / Linux 用户请改用 `--cookie` 手工提供（见下）。 |

> **不想用云端？** 也可以走本地模型（whisper.cpp），完全离线、免费，但要自己装引擎和模型，且慢得多。见 [`docs/backends.md`](docs/backends.md)。

## 快速开始

### Go 版

```bash
cd go
go build -o wx-channels-transcript ./cmd/sph    # 需要 Go ≥ 1.24

# 1. 自检：看看这台机器能走哪条路（会解释推荐理由）
./wx-channels-transcript scan

# 2. 提取
./wx-channels-transcript "https://weixin.qq.com/sph/xxxx" -o ./transcripts
```

> 交叉编译其他平台：`GOOS=windows GOARCH=amd64 go build -o wx-channels-transcript.exe ./cmd/sph`
> （纯 Go 实现，无 cgo，交叉编译不需要额外工具链）

### Node 版

```bash
cd node

# 1. 自检
wx-channels-transcript scan

# 2. 提取
wx-channels-transcript "https://weixin.qq.com/sph/xxxx" -o ./transcripts
```

### 产出

成功后你在输出目录得到两个文件：

- `YYYY-MM-DD-<标题>.md` —— 人读的逐字稿
- `YYYY-MM-DD-<标题>.json` —— 机器读的（含分句时间戳）

**stdout 只打印生成的 `.md` 路径**，进度信息走 stderr，便于脚本取用。

## 第一次运行会发生什么

视频号解析需要元宝登录态，而它通常已存在于你日常用的浏览器里。第一次读取时，macOS 会弹**一次**钥匙串授权：

- 要输入的是 **Mac 登录密码**（开机密码）——不是元宝密码，也不是 Apple ID
- 这是 macOS 的安全机制，不是本程序私自索取
- 只读取**会对 `yuanbao.tencent.com` 生效**的 cookie（按标准作用域匹配，含该域名及其父域 `.tencent.com`），**不会读取其他站点**
- 点「**始终允许**」后登录态会被缓存，**期间不再弹窗**（约一个月）

**不想授权？** 两个选择：

```bash
# 1. 手工提供 cookie（从浏览器 DevTools 复制），完全跳过钥匙串
<命令> "<链接>" --cookie "hy_token=xxx; hy_user=yyy; ..."

# 2. 全程用本地模型，不碰浏览器、不联网转写
<命令> "<链接>" -t local
```

> **给 Agent 的硬约束**：在触发浏览器 cookie 读取前，**必须先向用户说明**将读取元宝登录态、可能弹出系统密码框；用户拒绝时改用 `--cookie` 或 `-t local`。系统弹窗是 macOS 行为，不等于用户已经同意。

## 能力边界（建议先读）

- ⚠️ **不支持视频号直播和回放**。元宝接口只认已发布的视频。需要直播请用 MITM 抓包方案。
- ⚠️ **依赖腾讯的非公开接口**。腾讯改字段或加风控就会失效。代码里对字段名做了多重兜底，但无法免疫。
- ⚠️ **建议用小号元宝账号**。这是拿它调没对外承诺的接口。
- ⚠️ **音频内容会离开本机**（发给所选云端 ASR）。敏感素材请用 `-t local`，全程离线。
- ⚠️ **ASR 输出未经校对**。同音字可能有误（人名、英文专名、数字尤其），做精确引用前请核对原视频。
- ⚠️ **版权**：下载他人内容用于二次创作并对外发布，涉及原作者权利。内部拆解参考风险低，直接搬运有风险。

## 出问题怎么办

| 现象 | 原因 | 怎么处理 |
|---|---|---|
| `在 chrome 里没有找到 yuanbao.tencent.com 的 cookie` | 这个浏览器没登录过元宝 | 用该浏览器打开 https://yuanbao.tencent.com 扫码登录 |
| `读取到的 cookie 无法通过元宝校验（HTTP 401）` | 登录态过期（约一个月） | `<命令> auth --clear` 后重跑 |
| `拿不到 export_id` | 视频已删除 / 私密 / 分享链接过期 | 换一条链接。这是服务端返回的，不是程序 bug |
| `该链接不受支持` | 直播或回放 | 元宝接口不支持，需改用 MITM 方案 |
| 提示需要 ffmpeg | 选了本地模型（只有它需要） | 换默认的云端后端即可，或 `brew install ffmpeg` |

完整排错见 [`docs/troubleshooting.md`](docs/troubleshooting.md)。

## 进阶（第一次用可以先跳过）

### 热词：默认开启，不用管

视频号标题里往往就写着 ASR 最容易错的那批专名，工具会**自动提取并喂给 ASR**：

```
标题：如何把人字拖拍成买不起的样子 #广东 #拖鞋 #奢侈品
自动提取：广东、拖鞋、奢侈品
```

```bash
<命令> "<链接>" --show-hotwords          # 只看看会提取出哪些词
<命令> "<链接>" --hotwords "补充词1,补充词2"
<命令> "<链接>" --no-auto-hotwords       # 关掉自动提取
```

两个云端后端都**零配置支持热词**，不需要为了热词换后端。

### 保留音频 / 视频

**默认不产生任何中间文件**。需要媒体时显式开口：

```bash
<命令> "<链接>" --save-audio                      # 存音频
<命令> "<链接>" --save-audio --audio-format mp3   # 指定格式：wav / mp3 / m4a
<命令> "<链接>" --save-video                      # 存原始视频
<命令> "<链接>" --keep                            # 全都要
```

> ⚠️ **给 Agent**：除非用户明确要音频/视频文件，否则**不要主动加 `--save-audio` / `--save-video`**——那会多一次下载和 ffmpeg 转换。

### 本地文件也能转

不一定非得是链接，本地已有的音频/视频同样可以：

```bash
<命令> ./已有视频.mp4 -o ./transcripts
```

### 后端怎么选

| 后端 | 特点 |
|---|---|
| **阿里云百炼**（默认） | 零额外步骤（能直链直传、能直接吃 mp4），速度快 |
| **火山引擎** | 同样零额外步骤；热词直传方便。可用来交叉验证识别结果 |
| **本地 whisper.cpp** | 完全离线、免费；需自装引擎和模型，慢 |

完整对比、配置方法与官方文档链接见 [`docs/backends.md`](docs/backends.md)。

## 产出的数据流向

| 环节 | 数据 | 去向 |
|---|---|---|
| 解析 | 视频链接 + 元宝 cookie | **腾讯**（yuanbao.tencent.com） |
| 下载（仅在需要时） | — | **腾讯**（finder.video.qq.com） |
| 转写 | **音频内容** | **所选云端 ASR**（阿里云 / 火山） |

用 `-t local` 时音频不出本机。

## 文档索引

| 文档 | 看它做什么 |
|---|---|
| [`skill/SKILL.md`](skill/SKILL.md) | **给 AI Agent**：决策顺序、前置检查、失败处置、硬约束 |
| [`docs/backends.md`](docs/backends.md) | 配置 ASR Key；各后端能力对比与官方文档链接 |
| [`docs/troubleshooting.md`](docs/troubleshooting.md) | 排错 |
| [`docs/architecture.md`](docs/architecture.md) | 架构与设计原则 |
| [`docs/benchmarks.md`](docs/benchmarks.md) | 实测数据（含测试条件与样本量说明） |
| [`docs/research/`](docs/research/) | 模型选型调研 |
| [`docs/AUDIT.md`](docs/AUDIT.md) | 安全与代码质量审计报告 |
| [`go/NOTES.md`](go/NOTES.md) | Go 版实现笔记（与 Node 版的差异） |

## License

MIT
