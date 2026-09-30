# 架构与设计决策

## 三层结构

```
Skill（skill/SKILL.md）
  └─ 给 AI Agent 看：有什么能力、什么时候用、怎么决策、出错怎么办
        ↓
CLI（bin/sph.mjs + src/）
  └─ 真正干活。人和 Agent 都能用，可管道、可脚本化
        ↓
（未来可选）MCP 包装层
  └─ 只在需要接进「跑不了命令行的图形客户端」时才加
```

**为什么不做 MCP**（这是本项目最重要的一个决定）：

MCP 的价值只有四条——工具发现、参数校验、长任务状态、跨客户端统一。逐条对照：

| MCP 的价值 | 本项目 |
|---|---|
| 工具发现 | **Skill 已经做了**，而且更轻（一份 Markdown） |
| 参数校验 | 参数就一个链接，字符串而已，不需要 schema |
| 长任务状态 | 全程约 10 秒，没有"长任务" |
| 跨客户端 | 前提是「在用户自己的 Agent 上跑」，而 Agent 都会执行命令 |

结论：**MCP 在这里是多一层协议换零收益**，还要多背一个 `@modelcontextprotocol/sdk` 依赖。

参考项目 [video-extract-mcp](https://github.com/yanlingLabs/video-extract-mcp) 做成 MCP 是合理的——它定位通用工具，想被尽量多的客户端接入。本项目定位单一场景，Skill + CLI 更合适。

**CLI 是核心资产**：它同时服务三种消费者——你的终端、Agent 的 bash、以及未来可能的 MCP 包装层。MCP 只是它的一个薄壳，随时能加，不该是起点。

---

## 数据流

```
视频号分享链接
   │
   ├─① 登录态 ──────────► ~/.config/sph-transcript/session.json（缓存）
   │                         ↑ 优先复用；失效才重新读浏览器
   │
   ├─② 解析（腾讯元宝）
   │      POST /api/weixin/get_parse_result   链接 → wx_export_id
   │      POST /api/findergetobjecturl        export_id → CDN 直链
   │
   ├─③ 转写（按后端能力分两条路）
   │      A. 后端能直链直传 ──► 直接把 CDN 直链交给 ASR（不下载、不转码）
   │      B. 后端只收音频   ──► 下载 → ffmpeg 抽音 → 提交
   │
   └─④ 落盘
          YYYY-MM-DD-<标题>.md    给人读
          YYYY-MM-DD-<标题>.json  给机器读（含分句时间戳）
```

**关键优化**：阿里云百炼能直接吃 mp4 且接受公网 URL，所以走它时**整个 ③ 是零本地处理**——这也是它被排在推荐第一位的原因。实测解析 0.4s + 转写 8.7s。

---

## 几个刻意的取舍

### 1. 零 npm 依赖

只用 Node ≥ 22 的内置能力：
- `fetch`（HTTP）
- `node:sqlite`（读 Chrome cookie 库）
- `node:crypto`（AES 解密 + PBKDF2）

**收益**：拷一个目录就能跑，没有 `node_modules`，没有安装步骤，没有供应链风险面。
**代价**：本地模型推理必须走外部可执行文件（不能进程内推理），这是可接受的——本地本来就是兜底路径。

### 2. 优先云端，本地兜底

不是"云端更好"，而是**云端能省掉整条本地处理链**（下载、转码、模型、算力）。本地后端的价值在于"没有凭证时也能用"和"数据不出本机"，不是性能。

### 3. 把「为什么」讲出来

`sph scan` 不只报状态，还会解释推荐理由：

> 阿里云百炼已就绪。它既能接受公网直链、又能直接处理 mp4，所以整条链路可以做到「不下载、不转码」，是最快的一条。

这是刻意的——用户（和他自己的 Agent）需要理解**为什么**走这条路，才能在环境变化时自行调整。

### 4. 配置引导给链接，不给流程

各 ASR 后端的注册流程会变，官方文档链接不会。所以 `sph backends` 输出的是"缺什么 + 怎么做 + **官方文档链接**"，而不是把注册步骤写死在代码里。

---

## 踩过的坑（都值得记下来）

### 读 Chrome cookie 有三个暗礁

参考项目源码里针对第一个坑写过警告，我们三个都踩了一遍：

| # | 坑 | 现象 | 正解 |
|---|---|---|---|
| 1 | **手挑 cookie 名字** | 只读 `hy_token`/`hy_user` → 实际会话 cookie 未必叫这个，改名就静默失效 | 把该域名的 cookie **全部**发出，让服务端决定 |
| 2 | **按域名前缀过滤** | `LIKE '%yuanbao%'` → **漏掉挂在父域 `.tencent.com` 下的会话 cookie** | 做标准作用域匹配（host-only 精确 / 域后缀） |
| 3 | **二进制 cookie 的编码** | 某些 analytics cookie 是二进制，UTF-8 解码产生 U+FFFD → Node 的 `fetch` 直接抛 `TypeError: Cannot convert argument to a ByteString` | 用 **latin1** 解码 + 过滤控制字符 |

> 讽刺的是，坑 #2 正是"以为自己在避免坑 #1"时踩的——我按域名过滤的心智模型是"只读元宝的 cookie 更安全"，但**cookie 作用域本来就不等于域名前缀**。

### 各 ASR 后端的格式差异

| 后端 | 能直链直传 | 能吃 mp4 |
|---|---|---|
| 阿里云百炼 | ✅ | ✅ |
| 火山引擎 | ✅ | ✅（**实测**；官方文档白名单里没有 mp4，是我们试出来的） |
| 本地 whisper | ❌ | ❌ |

**实测的意义**：火山这条如果不实测，只看官方文档就会得出"需要 ffmpeg"的结论，从而在代码里多写一条转码路径、在文档里多警告一次。**文档 ≠ 实际能力。**

结果就是：**两个云端后端都是"零额外步骤"**，ffmpeg 只在走本地 whisper 时才需要。

### 百炼两个模型的结果结构不一样

```
qwen3-asr-flash-filetrans  → 请求 input.file_url（单对象），结果 output.result.transcription_url
qwen3-asr-flash-filetrans  → 请求 input.file_url（单对象），结果 output.result.transcription_url
fun-asr 等                 → 请求 input.file_urls（数组），结果 output.results[].transcription_url
```

混用会报 400 或拿到空结果。代码里用 `SINGLE_OBJECT_MODELS` 正则区分。

---

## 扩展新的 ASR 后端

在 `src/asr/` 下新建文件，导出一个对象：

```js
export default {
  id: 'mybackend',
  label: '显示名称',
  envKeys: ['MY_API_KEY'],           // 探测用
  acceptsUrl: true,                  // 能否吃公网 URL
  acceptsMp4: false,                 // 能否直接吃 mp4
  needsFfmpeg: false,
  detect(env) { return { available: true, detail: '...' }; },
  async transcribe({ url, file, model, language, onProgress }) {
    return { text, model, sentences: [] };
  },
};
```

然后在 `src/asr/index.mjs` 的 `ORDER` 数组里注册即可，推荐逻辑会自动把它纳入排序。

欢迎 PR 补齐腾讯云、讯飞、SenseVoice 等。
