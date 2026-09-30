# NOTES —— Go 版实现笔记

对照对象：``（Node 版 v0.3.0，2009 行）。
本文只记录**与 Node 版不一样的地方**、**Go 特有的坑**、**没做完/没验证的部分**。

---

## 1. 与 Node 版实现不同的问题

### 1.1 退出码体系换了（有意为之）

Node 版用的是「流水线序号」：`1` 参数 / `2` 解析 / `3` 未授权 / `4` 解析失败 /
`5` 无后端 / `6` 媒体准备失败 / `7` 转写失败。本实现按任务规格改成按**失败性质**分类：

| 码 | 含义 | 对应 Node 版 |
|---|---|---|
| 0 | 成功 | 0 |
| 1 | 参数或环境问题 | 1 |
| 2 | 解析 / 下载 / 媒体准备失败 | 2、4、6 |
| 3 | 登录态问题 | 3 |
| 4 | 未找到可用后端 | 5（scan 无后端时 Node 用的是 2） |
| 5 | 转写失败 | 7 |

**这是有意的行为差异**，脚本迁移时要注意。

### 1.2 时间戳用 UTC

一开始用 `time.Now().Format("2006-01-02")` 生成本地日期，实跑后发现与 Node 版
落在**不同的文件名**上：Node 的 `new Date().toISOString().slice(0,10)` 是 UTC 日期，
本机 UTC+8 时差会导致跨天。已改为 UTC，`extracted_at` 也统一成
`2026-09-30T16:10:20.527Z` 这种毫秒级 UTC 格式。现在两版在同一天产出同名文件。

### 1.3 timing 精度对齐到毫秒

Go 的 `time.Since(t).Seconds()` 是纳秒精度，写进 JSON 会得到 `0.600583916` 这种值，
而 Node 是 `(Date.now()-t0)/1000`，天然三位小数。已改成
`float64(time.Since(t).Milliseconds()) / 1000`。

### 1.4 JSON 的 HTML 转义

Go 的 `encoding/json` 默认把 `<` `>` `&` 转义成 `\u003c` `\u0026`，而视频号直链里
**全是 `&`**，导致 `--probe --json` 输出与 Node 完全不可比。新增 `internal/jsonout`，
用 `Encoder.SetEscapeHTML(false)` 统一关掉。

### 1.5 Cookie 头的顺序

Node 的对象字面量保持插入顺序，Go 的 map 无序。改成**按键名排序**输出，
保证同一份 jar 每次产出完全一致的字符串（便于比对缓存、便于测试）。
副作用：Cookie 头的顺序与 Node 不同。服务端不关心顺序，且同名的重复 cookie
在两版里都是「后写覆盖」，语义一致。

### 1.6 环境变量文件的引号剥除

Node 的判断是 `v.length >= 2 && v[0] === v[1] && v[0] 是引号` —— 比较的是**前两个字符**
是否相同，所以 `KEY="sk-abc"` 其实**不会**被剥引号（这是个 bug）。Go 版实现的是本意
（首尾同为引号才剥）。这是有意的修正。

### 1.7 本地文件 + `--save-audio` / `-t local`

Node 版在「输入是本地文件」时，`info.mediaUrl` 是 `null`，但如果后端需要本地媒体
（或用户要求留音频），仍会走 `prepareMedia(null, ...)` → `fetch(null)` 直接崩。
Go 版改成：本地文件不下载，抽音频直接从该文件抽。

### 1.8 duration 现在会填了（Node 版永远是「未知」）

Node 版的 `info.duration` 从未被赋值，所以 `duration` 字段恒为「未知」。
任务规格里明确要求 `duration: <N 分 N 秒>`，因此本实现从服务端取：

- 百炼：任务响应顶层的 `usage.seconds`（实测这个字段就是音频秒数，如 433）
- 火山：`output.result.audio_info.duration`（毫秒）
- 都拿不到时：用最后一句话的 `end_time`（毫秒）兜底

百炼的转写结果 JSON 里 **没有** 总时长（`audio_info` 只有 `format`/`sample_rate`），
这一点是实测确认的 —— 必须从任务响应里取，不能从结果 JSON 里找。

### 1.9 `--probe --json` / `--show-hotwords` 的键顺序

内容一致，但 Go 用 map 序列化，键按字典序输出（`author` 在前而不是 `title`）。
值本身完全相同。

---

## 2. Go 特有的坑

### 2.1 `defer` 与 `os.Exit` 不能共存 —— 决定了整个程序的骨架

中间文件清理、cookie 库副本删除都靠 `defer`。而 `os.Exit` **不会执行 defer**。
所以整个程序里**只有 `main()` 末尾有一次 `os.Exit`**，所有流程都写成
`cli.Run(argv) int` 并返回退出码，保证所有 defer 都能跑到。

### 2.2 AES-CBC 的 PKCS7 必须自己解

Node 的 `createDecipheriv('aes-128-cbc', ...)` 默认开启 padding 校验，padding 不合法就抛错
（被 `catch` 成 `null` → 跳过这条 cookie）。
Go 的 `cipher.NewCBCDecrypter` **完全不管 padding**，直接吐出带填充的明文。
所以手写了 `pkcs7Unpad`，并且必须把「padding 非法」当成**解密失败**处理
（返回空串），否则会把垃圾字节当成 cookie 值发出去。

### 2.3 SQLite 驱动

用 `modernc.org/sqlite`（纯 Go，无 cgo，交叉编译友好），打开时带
`file:<path>?mode=ro&immutable=1`。它是本次唯一的直接三方依赖，但会带进
`modernc.org/libc` 等一批间接依赖（同一个纯 Go 项目，无法避免）。
二进制因此有 14MB —— 这是纯 Go SQLite 的代价，换来的是不需要 cgo、可以 `GOOS=linux go build`。

### 2.4 `crypto/pbkdf2` 进了标准库（Go 1.24）

PBKDF2-SHA1 直接用了标准库的 `crypto/pbkdf2`，因此 `go.mod` 里写 `go 1.24`，
这也让「除 sqlite 外零依赖」这条约束得以成立（否则得引 `golang.org/x/crypto`）。

### 2.5 `http.Client` 没有默认超时

每个请求都单独包了 `context.WithTimeout`（解析 15s、提交 60s、轮询 30s、下载 30min），
否则一旦对端不响应会永久挂住。

### 2.6 二进制 cookie 值

Go 的 `string` 天然是字节串，不需要任何转码 —— 这点比 Node 省事（Node 的
`fetch` 只吃 ByteString，必须用 latin1）。唯一要做的还是洗掉控制字符
`\x00-\x08`、`\x0a-\x1f`、`\x7f`（保留 `\t`），否则 `net/http` 会拒绝发送该请求头。

### 2.7 命令行解析手写

Go 的 `flag` 包遇到第一个非选项参数就停止解析，做不到 `sph <链接> -o ./out`
这种「选项在后」的写法，也做不到 `-o` 与 `--out` 同时映射到同一个值。
所以按 Node 版的语义手写了一遍（`internal/cli/args.go`）。

---

## 3. 未完成 / 未验证的部分（如实说明）

### 3.1 ⚠️ 读浏览器 cookie（钥匙串）这条路径**没有实机跑过**

原因：读取 Chrome 主密钥会**弹出 macOS 钥匙串授权窗口**，需要用户在场点确认，
我在无人值守的情况下不应该触发它。

为了不让这块成为盲区，写了 `internal/cookies/chrome_test.go`，用 AES-128-CBC 按 Chrome
的方案**加密**了几条测试 cookie 写进临时 SQLite，然后走完整读取链路：
`modernc 读库 → v10 解密 → PKCS7 去填充 → 剥 32 字节 domain hash → 洗控制字符 → 作用域匹配`。
覆盖了三个已踩过的坑（不按名字挑、父域 `.tencent.com` 要能匹配、二进制值不能丢）。

**未覆盖的只剩**：`security find-generic-password` 取密码这一步（一行 exec）
和 `findCookieDB` 的真实路径。已确认本机钥匙串里有 `Chrome Safe Storage` 条目，
且 Chrome 的 cookie 库确实在 `Default/Cookies`（回退路径，不是 `Default/Network/Cookies`），
与 `findCookieDB` 的候选顺序一致。

### 3.2 `volcengine-small` 后端未实测

本机缺 `VOLC_ASR_APP_ID` / `VOLC_ASR_ACCESS_TOKEN` / `VOLC_ASR_CLUSTER` 三件套。
代码照 Node 版移植（老接口无公开稳定 schema，解析是宽松的），**执行路径没有跑过**。

### 3.3 本地 whisper 后端未实测

本机没装 whisper.cpp，也没有模型文件。`Detect()` 正确报「没有找到本地 whisper 可执行文件」，
但 `Transcribe()` 没跑过。不过本地文件→百炼的**上传通道**是跑通的（见第 4 节），
两者共用同一套「先拿到本地文件」的逻辑。

### 3.4 `--vocabulary-id` 未实测

需要一个真实的百炼热词表 ID。而且这个参数「传错了也不会报错」（任务照常 SUCCEEDED，
参数被静默忽略），所以**必须靠比对实际输出来验证**，没法在自测里确认。代码路径与 Node 一致。

### 3.5 `--browser <名称>` 未实测

同 3.1。`DetectBrowsers()` 已确认本机能列出 Chrome / Chromium / Edge / Brave / Arc / Vivaldi / Opera 七个。

### 3.6 上传通道只在小文件上验证过

百炼「临时上传」通道是在一个 23MB 的 mp4 上跑通的（含中文+空格的原始文件名，
验证了文件名净化）。超大文件、超长时长的边界没试。

---

## 4. 实际验证结果

编译与静态检查：`go build ./...`、`go vet ./...`、`gofmt -l .` 全部干净。
单元测试：`go test ./...` 全绿（cookies / hotwords / output / resolve 四个包）。

真机跑通的场景：

| 场景 | 结果 |
|---|---|
| `sph --help` / `--version` | ✅ |
| `sph scan` / `scan --json` | ✅ 正确识别出 DASHSCOPE_API_KEY 与 VOLC_TTS_API_KEY，推荐百炼 |
| `sph backends` | ✅ 三个后端，含未配置的本地后端与配置指引 |
| `sph auth` / `auth --clear` | ✅ 能读 Node 版格式的缓存并校验（HTTP 200） |
| `sph <链接> --probe` | ✅ 拿到 export_id 与 finder.video.qq.com 直链 |
| `sph <链接>`（百炼直链直传） | ✅ 2515 字，10 秒左右 |
| `sph <链接> -t volcengine` | ✅ 2547 字，热词生效，「苏姿丰」零误识 |
| `sph <链接> -m fun-asr` | ✅ 走 `file_urls` 数组 + `results[0]` 分支 |
| `sph /tmp/本地\ 视频.mp4`（百炼临时上传） | ✅ 中文+空格文件名，`oss://` + `X-DashScope-OssResourceResolve` |
| `sph <链接> --save-video` | ✅ 下载 22.7MB 并保留 |
| `sph ./x.mp4 --save-audio --audio-format mp3` | ✅ ffmpeg 抽音并保留 |
| 退出码（1/3/4 各路径） | ✅ 与预期一致 |
| 临时文件残留 | ✅ `/tmp` 下无 `sph-*` / `sph-cookies-*` 残留 |

与 Node 版对同一个链接的产出对比：`.md` 头部**逐字节一致**（除 duration 与耗时数字），
`.json` 结构等价（键顺序、timing 精度差异见 1.3 / 1.9）。
