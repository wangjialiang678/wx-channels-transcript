# 独立审计报告

**审计日期**：2026-09-30
**审计对象**：``（v0.3.0，共 11 个源码文件 + 5 份文档 + 1 份 SKILL）
**审计方法**：逐文件通读 + 关键函数实机执行验证（`slugify` / `hostMatches` / `looksLikeMediaUrl` 已跑过）+ 文档与代码逐条交叉比对

**审计结论**：**架构与取舍方向是对的，但"实测过"的自信已经溢出到文档里——至少 6 处文档描述与代码实际行为相反（火山能否吃 mp4、是否支持本地文件、本地热词是否实现），外加 1 个真实的隐私残留（Chrome cookie 临时库不删除）。这批不一致会直接误导使用它的 AI Agent 去做错误决策，是当前最大的问题，比代码本身严重。开源前必须先做一轮"文档对齐代码"，否则等于把错误的操作说明发给用户。**

---

## 严重问题（会导致损失或安全风险）

### S1. 复制出来的 Chrome cookie 数据库从不删除，`/tmp` 下持续残留

**文件**：`src/chrome-cookies.mjs:127-131`

```js
const tmp = join(mkdtempSync(join(tmpdir(), 'sph-cookies-')), 'Cookies');
copyFileSync(db, tmp);
const conn = new DatabaseSync(tmp);
...
conn.close();          // ← 只关了连接，tmp 目录与文件副本永远留在磁盘
```

- `mkdtempSync` 在系统临时目录创建一个目录，把用户的 `Cookies` 库完整复制进去，**全程没有任何 `rmSync`/`unlinkSync`**（全仓库 grep 确认：`rmSync`/`unlinkSync` 只出现在 `media.mjs` 和 `local.mjs`，`chrome-cookies.mjs` 一处都没有）。
- 后果：每运行一次（且每次缓存失效都会重跑）就在 `/tmp/sph-cookies-XXXX/Cookies` 留一份**用户浏览器全量 cookie 的加密数据库副本**（含 host_key 列表与加密值）。这是本工具唯一"把浏览器数据复制到磁盘"的地方，却恰恰没清理。
- 附带：`decryptValue` 解出的是明文值，虽然不落盘，但同一进程里已经持有全部 `.tencent.com` 作用域 cookie。

**建议**：`readBrowserCookies` 用 `try { … } finally { rmSync(dirname(tmp), { recursive: true, force: true }) }` 包住；`mkdtemp` 的返回值单独存变量，别只留拼接后的文件路径。

### S2. 把 `NODE_TLS_REJECT_UNAUTHORIZED=0` 说成"对本工具无害"，实为重大风险且代码无任何防护

**文件**：`docs/troubleshooting.md:119-123`

> 它让 Node 跳过 TLS 证书校验。**对本工具无害**（我们只在访问腾讯/阿里/你配置的 ASR 时用到）

- 事实相反：本工具会 (1) 用该连接把**元宝 cookie 的明文**发给 `yuanbao.tencent.com`（`src/session.mjs:58-70`、`src/resolve.mjs:56-64`），(2) 把**用户音频**上传到阿里云临时存储（`src/asr/bailian.mjs:206`）、并把直链 URL 交给 ASR。关闭证书校验 = 中间人可完整窃取 cookie 与音频内容，恰恰是本工具最敏感的三类数据。
- 代码里没有任何地方检测或拦截这个变量（全仓库无 `NODE_TLS_REJECT_UNAUTHORIZED` 引用），却用文档给出"无害"背书。

**建议**：删掉"无害"表述，改为明确警告；在 `bin/sph.mjs` 启动时检测到该变量即强提示（或直接拒绝运行，除非显式 `--insecure`）。

### S3. 文档反复承诺"支持本地音频/视频文件"，代码根本不支持——属虚假能力声明

**文件**：`skill/SKILL.md:3`（frontmatter 触发条件）、`skill/SKILL.md:24`

> 也支持本地音频/视频文件。 / 本地已有音频/视频文件，想转成文字

**代码**：`bin/sph.mjs:199-205`

```js
const input = positional[0];
if (!input) { … }
if (!parseShareId(input)) {
  log(`错误：这不是可识别的视频号链接：${input}`);
  process.exit(1);           // ← 本地文件路径在这里被直接拒绝
}
```

- `parseShareId`（`src/resolve.mjs:29-38`）只认 `weixin.qq.com/sph/…`、`channels.weixin.qq.com/?id=…`、或纯 `[A-Za-z0-9_-]{6,}` 串。任何形如 `/Users/x/a.mp4` 的路径必然返回 `null` → 立即 `exit(1)`。
- 全流程**没有任何**"输入是本地文件"的分支（`resolve()` 是唯一入口，必然走腾讯接口）。
- 由于这是 SKILL 的触发条件之一，AI Agent 会照文档把本地文件丢进来，然后稳定失败。

**建议**：要么在 `bin/sph.mjs` 增加"输入存在且是文件 → 跳过解析、直接进入转写"的分支；要么把 SKILL/README 里"支持本地文件"的表述删干净。

---

## 中等问题（影响可用性/可维护性）

### M1. 输出里的"时长"永远是"未知"

**文件**：`src/output.mjs:40`、`src/output.mjs:52` 引用 `info.duration`；但 `src/resolve.mjs:135` 返回的对象只有 `{ shareId, exportId, title, author, mediaUrl }`，`bin/sph.mjs:247` 也只是 `{ ...r, url: input }`。全仓库无任何地方给 `info.duration` 赋值（grep 确认）。
**结果**：每份 `.md` 的 frontmatter 和正文都写"时长：未知"。
**建议**：`resolve` 里从接口字段（如 `audio_info.duration`）补齐 duration，或彻底删掉这两个占位输出，别输出一个永远错的字段。

### M2. "本地 whisper 用初始提示词实现热词"——从未实现

**文件**：`README.md:114`、`skill/SKILL.md:90`、`src/hotwords.mjs:76-79` 与 `86-90` 均称本地后端用 `--prompt` 近似热词。
**代码**：`src/asr/local.mjs:102` 的 `transcribe` 参数根本不接收 `hotwords`；`bin/sph.mjs:372-376` 虽然把 `...hotOpts`（含 `hotwords`）传进去，但被静默忽略；`local.mjs:119-120` 构造的 whisper 参数里没有 `--prompt`。
**结论**：本地路径下热词功能不存在。
**建议**：在 `local.mjs` 真正拼上 `--prompt`，或把三处"本地热词"表述改成"不支持"。

### M3. 媒体 URL 白名单形同虚设（`looksLikeMediaUrl` 太松）

**文件**：`src/resolve.mjs:84-90`。实测该函数接受以下**非腾讯**地址（我已实机跑过）：

```
https://evil.com/?u=.video.qq.com        → true
https://evil.com/finder.video.qq.com/x   → true
https://x.video.qq.com.evil.com/a        → true
https://attack.tc.qq.com.evil.net/a.mp4  → true
http://127.0.0.1/a.mp4                    → true
https://example.com/malware.mp4          → true
```

- 原因是它对**整个 URL 字符串**做 `/(finder\.video\.qq\.com|\.video\.qq\.com|\.tc\.qq\.com|mmfinder)/` 子串匹配（path/query 里出现也算），且"任何主机上的 `.mp4`"直接放行。
- 危害有限（URL 来自腾讯接口响应），但它**不是**注释里宣称的"域名白名单"，一旦腾讯响应字段被污染或将来接入其它源，会把下载指向任意主机（配合 `media.mjs:43` 的默认跟随重定向）。

**建议**：`const u = new URL(s)` 后用 `u.hostname` 做后缀精确匹配（`hostname === 'finder.video.qq.com' || hostname.endsWith('.video.qq.com') || hostname.endsWith('.tc.qq.com')`），别再对整串跑正则。

### M4. `-t volcengine-small` 是死路径，文档却把它当可用备选

**文件**：`src/asr/index.mjs:19`（注册进 `BACKENDS`）、`src/asr/index.mjs:34`（注释称"需要时可显式 `-t volcengine-small` 调用"）；但 `src/asr/index.mjs:39` 的 `ORDER` 不含它。
`bin/sph.mjs:293-298` 用 `detected.find(x => x.id === backend.id)` 判断可用性——由于 `detectAll` 只遍历 `ORDER`，`volcengine-small` 永远 `undefined` → `d?.available` 为 `undefined` → 打印"在本机不可用：未知原因"并 `exit(5)`。
**文档还把它当核心卖点**：`docs/backends.md:148,154,170-180`，`src/asr/volcengine.mjs:17-21`。
**建议**：把 `volcengine-small` 加进 `ORDER`（但 `detect` 的 legacy 凭据与 `volcengine` 冲突需处理），或放弃该后端并从文档移除。

### M5. `--save-audio --audio-format mp3` 时，送给 ASR 的音频不是 16k 单声道

**文件**：`src/media.mjs:101-102`

```js
// ASR 需要 16k 单声道；若非 wav 且仅用于 ASR，也统一成 wav 更稳
const asrSpec = audioFormat === 'wav';   // ← 只有 wav 才加 -ac 1 -ar 16000
extractAudio(mediaPath, audioPath, { format: audioFormat, forAsr: asrSpec, … });
```

注释说"非 wav 也统一成 wav 更稳"，代码却恰恰相反：`mp3/m4a` 时 `forAsr=false`，`extractAudio`（`media.mjs:59`）不加 `-ac 1 -ar 16000`。当后端需要本地音频（火山小模型/本地）而用户又要 mp3 时，上传的是未经 ASR 规格归一化的音频。
**建议**：ASR 输入与"用户留存格式"解耦——ASR 输入恒为 16k 单声道 wav，用户留存再另转目标格式。

### M6. 本地后端 `-m/--model` 会被当成文件路径传给 whisper-cli

**文件**：`bin/sph.mjs:374` 对本地分支取 `flags.model || flags.m || backendInfo.model`，而 `backendInfo.model`（`local.mjs:98`）是 **whisper 模型文件的绝对路径**。若用户按帮助文档（`bin/sph.mjs:82`）传 `-m whisper-small`，则该字符串原样进 `local.mjs:119` 的 `-m whisper-small`，whisper-cli 会因找不到该文件而失败。
**说明**：`local.mjs:58` 的 `models: ['whisper-small','whisper-medium','sensevoice']` 是"展示用名称"，与实际路径语义不一致。此外 `local.mjs` 完全没有实现 `language`→非 zh 时用 `-l auto` 之外的处理，`sensevoice` 也不在 `ENGINES` 里。
**建议**：本地后端要么仅接受文件路径（拒绝名称），要么加一层"名称 → 路径"映射。

### M7. 大量静默失败，异常被吞后表现为无关错误

**文件**：
- `src/resolve.mjs:67` `try { json = JSON.parse(text) } catch { /* 非 JSON 响应 */ }`
- `src/chrome-cookies.mjs:101-103` `decryptValue` 解密失败直接 `return null`
- `src/session.mjs:93-95` `loadCache` 解析失败返回 `null`
- `src/media.mjs:123,131` 清理失败静默

最影响排障的是 `decryptValue`：v10/v11 之外的前缀、或解密抛错，都返回 `null`，该 cookie 被静默丢弃。用户看到的最终错误是"没找到 cookie / 校验失败"（`session.mjs:157,164`），**完全无法知道其实是解密失败**（例如 Chrome 升级到 App-Bound 加密 v20 后必然全 null）。
**建议**：`decryptValue` 返回带原因的结果或 `onProgress` 上报；`loadCache` 失败时区分"文件不存在"与"文件损坏"。

### M8. 中断（Ctrl-C）/未捕获异常时临时目录残留

**文件**：`bin/sph.mjs:220`（`workDir = /tmp/sph-<ts>`）、清理只发生在 `bin/sph.mjs:338,380,404` 三条**已知**分支。
用户在下载/推理途中按 Ctrl-C，或发生未捕获异常，`/tmp/sph-*` 会留下 `source-*.mp4` / `audio-*.wav`（可能几十 MB）。`media.mjs:110-117` 的 `promote` 也是"复制"语义，中断在复制中途会留半个文件。
**建议**：注册 `process.on('SIGINT'|'SIGTERM'|'uncaughtException')` 钩子做 `cleanupDir(workDir)`；或改用 `fs.mkdtempSync` + 启动时清理陈旧目录。

### M9. 默认语言被 CLI 硬编码为 `zh`，覆盖了后端默认值

**文件**：`bin/sph.mjs:362,368,375` 一律 `flags.lang || flags.l || 'zh'`；而 `src/asr/volcengine.mjs:85` 的模块默认是 `'zh-CN'`。火山大模型线拿到的是 `zh` 而非 `zh-CN`，是否为合法入参未在代码中校验。
**建议**：把各后端的默认语言下沉到后端，CLI 只在用户显式指定时覆盖。

### M10. `--keep` 的语义与实现不符

**文件**：`bin/sph.mjs:403-404`

```js
if (!flags.keep) cleanup(tmpPaths);
cleanupDir(workDir);     // ← 无条件，包括 --keep 时
```

`--keep` 本意"保留全部中间文件"，但第 404 行无条件删掉整个 `workDir`。目前能"看起来生效"只是因为 `promote`（`bin/sph.mjs:393-400`）在 404 之前把音频/视频**复制**到了输出目录。`tmpPaths` 的清空与否成了无意义的分支。
**建议**：`--keep` 时跳过 `cleanupDir(workDir)`，或改名为 `--copy-media` 以匹配真实语义。

### M11. 无任何测试，关键纯函数不可回归

全仓库无 `test/`、无 `*.test.mjs`、无 `package.json`（`ls -a` 确认）。
`slugify`、`hostMatches`、`extractHotwords`、`buildCorpusText`、`looksLikeMediaUrl` 都是**无 IO 的纯函数**，恰恰最容易写单元测试——尤其 `looksLikeMediaUrl` 已经存在真实缺陷（见 M3），有测试就能挡住。
**建议**：用 node 内置 `node --test`（零依赖，符合项目原则）补最小测试集，优先覆盖上述 5 个函数。

---

## 轻微问题（整洁度）

| # | 位置 | 问题 |
|---|---|---|
| L1 | `bin/sph.mjs:21,22` | `existsSync`、`spawnSync` 导入后从未使用（grep 确认全文件仅这一处）。 |
| L2 | `src/asr/local.mjs:10` | `execFileSync` 导入未使用。 |
| L3 | `src/resolve.mjs:147-151` | `fetchComments` 导出但全仓库零调用（死代码，且注释自认"默认不用"）。 |
| L4 | `src/hotwords.mjs:63-83 / 86-90 / 113-115` | `toBackendPayload`、`HOTWORD_CAPABILITY`、`capabilityOf` 三个导出**全仓库零引用**（死代码）。且 `toBackendPayload` 对 bailian 的注释（第 72-74 行）仍写"需要预先创建的词表 ID"，与现行 `bailian.mjs:91-102` 的 `corpus.text` 优先实现不符。 |
| L5 | `src/asr/index.mjs:88` | `if (d.index === 0 \|\| d.id === 'bailian')`——`detected` 对象没有 `index` 字段，`d.index === 0` 恒为 false。无害但是无效条件。 |
| L6 | `src/hotwords.mjs:15` | `extractHotwords(title, { extra, max })` 的两个选项在唯一调用处（`bin/sph.mjs:268`）都没传，属未使用形参。 |
| L7 | `skill/SKILL.md:5` vs `bin/sph.mjs:33` | SKILL 版本 `0.1.0`，CLI 版本 `0.3.0`，不一致。 |
| L8 | `bin/sph.mjs:388` vs `src/output.mjs:30` | 两处各自用 `stamp + slugify(title, shareId)` 拼同一套文件名（一处给 `promote`，一处给 `writeOutputs`），逻辑重复、易分叉。 |
| L9 | `README.md:92` | 自动提取热词的示例输出里含 `WolrdLabs`（标题原文的拼写错误），即工具会把标题里的**错拼**当热词喂给 ASR。README 却把它当作"正好"的正面例子，未提示这层副作用。 |

---

## 文档与代码的不一致清单（逐条列出，含文件行号）

> 这一节是本报告的核心。以下每条都已对照代码逐行核实。

### 不一致 1（最严重）：火山引擎"能不能直接吃 mp4 / 要不要 ffmpeg"——文档自相矛盾且与代码相反

| 来源 | 表述 | 与代码是否一致 |
|---|---|---|
| 代码 `src/asr/volcengine.mjs:63` | `acceptsMp4: true`（大模型线） | 基准 |
| 代码 `src/asr/volcengine.mjs:65` | `needsFfmpeg: false` | 基准 |
| 代码 `src/asr/index.mjs:52-53` | `extraSteps` 只在 `!acceptsMp4` 时加"抽音频" → 火山**零额外步骤** | 基准 |
| `skill/SKILL.md:115` | 火山"直接吃 mp4 **❌ 只收音频**／需 ffmpeg 抽音" | ❌ 相反 |
| `docs/architecture.md:109` | 火山"能吃 mp4 **❌ 只收 raw/wav/mp3/ogg**" | ❌ 相反 |
| `README.md:73` | "默认 + 火山/本地 → 下载视频 ✅、调 ffmpeg ✅" | ❌ 相反 |
| `docs/backends.md:20` | "已有火山引擎账号 → 用火山，但**需要 ffmpeg 抽音频**" | ❌ 相反 |
| `docs/backends.md:147,153` | 火山大模型"能吃 mp4 **❌**""需要 ffmpeg 抽音频" | ❌ 相反 |
| **同一份** `docs/backends.md:192` | "音频格式（**实测口径**）：**mp4 也可直接跑** ✅" | ⚠️ 与上面三行自相矛盾 |
| **同一份** `docs/backends.md:207-219` | "火山大模型能吃 mp4——官方文档的白名单是错的……`needsFfmpeg` 已改为 false" | ⚠️ 与上面自相矛盾 |
| `README.md:124` | 火山 seedasr"直接吃 mp4 **✅**" | ⚠️ 与 README:73 自相矛盾 |

**影响**：SKILL 是给 Agent 读的决策依据。Agent 会据此认为火山必须下载 + 转码，从而在"本可直接直传"的情况下多做两步，甚至因为本机没 ffmpeg 而误判火山不可用。**同一份 backends.md 内部前后打架**，必须整篇重写这一项。

### 不一致 2：`-t volcengine-small` 文档可用、代码不可用

- `src/asr/index.mjs:34` 注释："需要时可显式 `-t volcengine-small` 调用"。
- `docs/backends.md:148,154,170-180`：把 `volcengine-small` 作为"唯一官方支持 mp4 的备选"详细介绍配置。
- 实际 `src/asr/index.mjs:39` 的 `ORDER` 不含它 → `bin/sph.mjs:293-298` 必然报"未知原因不可用"并 `exit(5)`。

### 不一致 3：本地音频/视频文件

- 文档称支持：`skill/SKILL.md:3`、`skill/SKILL.md:24`。
- 代码不支持：`bin/sph.mjs:201-205` 强制 `parseShareId`，本地路径即报错退出。（详见 S3）

### 不一致 4：本地后端热词

- 文档称"初始提示词近似"：`README.md:114`、`skill/SKILL.md:90`、`src/hotwords.mjs:76-79,86-90`。
- 代码未实现：`src/asr/local.mjs:102,119` 无 `--prompt`，`hotwords` 被忽略。（详见 M2）

### 不一致 5：阿里热词"是否要预建词表"——同一份 README 内部矛盾

- `README.md:112`："`parameters.corpus.text` —— 接受**任意文本**……无需预建词表"；`README.md:116-117` 还专门讲 corpus.text 的坑。
- 但 `README.md:123` 后端表把阿里热词标为"**需预建词表**"；`README.md:130` 又称"阿里侧得先去控制台建词表"。
- 代码：`src/asr/bailian.mjs:91-102` 优先走 `parameters.corpus.text`（无需词表），仅在无 corpus 时才用 `vocabulary_id`。
- 参考：`docs/backends.md:122-125` 已更新为"仍支持，但已非首选"（这一处是对的）。
- **结论**：README 的 108-117 行与 121-130 行互相打脸，`README.md:123` 与 `README.md:130` 属残留旧表述。

### 不一致 6：默认模型名

- 文档写 `qwen3-asr`：`README.md:123`、`README.md:135`、`docs/backends.md:57`（后者标注为 `qwen3-asr-flash-filetrans`，对）。
- 代码实际：`src/asr/bailian.mjs:31` `models: ['qwen3-asr-flash-filetrans', 'fun-asr']`，默认 `qwen3-asr-flash-filetrans`。
- `README.md:123` 的"qwen3-asr"是不存在的模型 id。

### 不一致 7：解析用了"3 个接口"

- `README.md:25` "解析（3 个接口）"；`src/resolve.mjs:4` docstring 也写"三步走"。
- 实际 `resolve()` 只发 **2 个 HTTP 请求**：`PARSE_URL`（`resolve.mjs:107`）与 `OBJECT_URL`（`resolve.mjs:124`）；`resolve.mjs:15-16` 也只定义了两个端点。第 3 步"直链自带签名"不是接口调用。
- 若把 `session.verify` 的 `getuserinfo` 算进去才是 3 次请求，但那不属于"解析"且发生在更早阶段。

### 不一致 8：中间文件的位置

- `docs/troubleshooting.md:129`："如果你看到了 `source-*.mp4` / `audio-*.wav`，说明走的是『需要下载』的后端……这些文件保留下来便于排查"——暗示在**输出目录**（该节标题即"输出目录里有一堆中间文件"）。
- 实际：中间文件在系统临时目录 `workDir = join(tmpdir(), 'sph-<ts>')`（`bin/sph.mjs:220,328`），且在成功路径被 `cleanupDir` 删除（`bin/sph.mjs:404`），**根本不会出现在输出目录，也不会被保留**。该节与行为完全相反。

### 不一致 9：`[sph]` 日志前缀

- `docs/troubleshooting.md:143`："完整的报错输出（stderr 里带 `[sph]` 或步骤编号的部分）"。
- 代码中 `log`/`out`（`bin/sph.mjs:58-59`）从不输出 `[sph]` 前缀（只有 `①②③` 步骤编号）。

### 不一致 10：browser 读取范围与"只读一个域名"的措辞

- `README.md:143`、`README.md:186` 前文、`src/session.mjs:35-36` KEYCHAIN_NOTICE："只读取 `yuanbao.tencent.com` 这一个域名下的 cookie"。
- 实际 `hostMatches`（`src/chrome-cookies.mjs:37-43`）按**标准 cookie 作用域**匹配，会一并收下 host_key 为 `.tencent.com` 等的**父域 cookie**（代码注释 `chrome-cookies.mjs:13-15` 自己也承认必须收 `.tencent.com`）。措辞"只读这一个域名"会让人以为收不到其它腾讯域 cookie，与实际作用域不符（虽然这是必要且正确的行为）。
- 建议：改成"只读取对 `yuanbao.tencent.com` 生效的 cookie（含其父域 `.tencent.com`）"。

### 不一致 11：`--probe` 未在 README 出现

- `skill/SKILL.md:65` 有 `--probe`；`bin/sph.mjs:97,261-265` 实现了它。
- `README.md` 的选项说明里**没有 `--probe`**（README 只列到 `--keep` 等）。文档覆盖不全（README 与 SKILL 选项集不一致）。

### 不一致 12：`paraformer` 与 `vocabulary_id` 的残留表述

- 代码已移除 paraformer：`src/asr/bailian.mjs:31` 的 `models` 只剩两个；`src/asr/volcengine.mjs` 无关。
- 仍残留提及 paraformer 的位置：
  - `src/asr/bailian.mjs:13`（头注释的请求体结构示例里列 `paraformer-v2`）——**代码注释残留**。
  - `docs/architecture.md:118`（`file_urls` 结构示例同样以 `paraformer-v2` 举例）——**已不支持的模型出现在示例里**，建议换成 `fun-asr`。
  - `docs/backends.md:62-77,229`、`README.md` 无、`docs/research/*`：属于"为何移除"的说明性内容，**保留合理**。
- `vocabulary_id`：`docs/backends.md:122-125` 已更新为"仍支持，但已非首选" ✓；但 `src/hotwords.mjs:60`（`toBackendPayload` 上方注释）仍写"阿里 → vocabulary_id，需先建词表拿 ID"，属未同步的旧说法（该函数本身也是死代码，见 L4）。

### 不一致 13：README 后端总表遗漏本地与小模型后端

- `README.md:121-124` 的"支持的语音识别后端"表只列阿里、火山 2 行，**没有"本地 whisper"**（而 README:163、`README.md:73` 都提到本地）也未提 `volcengine-small`。
- `bin/sph.mjs:69` 的 `sph backends` 实际会列 3 个（`ORDER`）+ 注册的 `volcengine-small`。

---

## 数字准确性核查

| 数字 | 出处 | 核查结论 |
|---|---|---|
| 0.4s / 8.7s / 9.4s | `README.md:25-27`、`docs/architecture.md:56` | 单条 433s 视频、单次运行，无重复测量/无方差；且 0.4+8.7=**9.1**，与"合计 9.4 秒"对不上（多出的 0.3s 未说明，可能是标题/热词/文件落盘）。 |
| 92.9% / 93.0% / 10.5% | `README.md:123`、`docs/backends.md:68-69,227-229`、`src/asr/index.mjs:37` | 标称"14 个判据点"，但算式用的是"39 对/3 错""40 对/3 错""4 对/34 错"，分母分别为 42/43/38——**判据点数与配对数口径不一致**。样本为 **n=1 视频**，不足以支撑"准确率"这种统计表述。 |
| 34.5s（火山） | `README.md:124`、`docs/backends.md:228` | 同为 n=1。"慢 4 倍"（34.5/8.7≈3.97）算法上成立，但基于单次测量。 |
| 苏姿丰 8 对 2 错 → 14 对 0 错 | `README.md:99`、`SKILL.md:82`、`backends.md:104` | 三处数字一致 ✓；但同样是小样本，且未说明统计方法。 |
| 热词 5 个（含 `WolrdLabs`） | `README.md:92` | 输出示例含拼写错误词 `WolrdLabs`（`extractHotwords` 的 `/[A-Z][A-Za-z0-9]+/` 会捕获它），文档未提示该副作用。 |

**总体**：所有性能/准确率数字都**标注了测试条件**（"433 秒视频，2026-09-30"），这点做得比多数项目好；但都是**单样本、无重复、无置信区间**，而文档用它们的口吻是确定性的（`SKILL.md:118`"这条路实测约 9 秒"会被 Agent 当成普遍承诺）。建议统一加一句"单条样本、仅供参考"。

---

## 死链 / 无效引用核查

- **确认有效**：`https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin`（实测 302 到 CDN，文件存在）；`https://github.com/SpeechColab/Leaderboard/`（实测 200，仓库存在）。仓库内部相对引用 `docs/architecture.md`、`docs/backends.md`、`docs/troubleshooting.md`（`SKILL.md:170-172`）均存在 ✓。
- **无效 / 外部依赖**：`docs/backends.md:38` 引用 "`alibaba-bailian-asr` skill 的 `references/model-selection.md`"——**该路径不在本仓库内**，外部读者无从获取，属死引用；应改为直接给阿里官方文档链接。
- **无法独立验证**：`docs/research/*.md` 引用了大量 2026 年的 arXiv 编号（如 2601.21337、2603.10420）与厂商微信稿，审计无法在本环境逐一确认真实性，建议发布前人工点一遍。
- **未逐一验证**：README/backends 里的 20+ 个第三方 GitHub / 厂商链接（`res-downloader`、`wx_channels_download`、火山 console 链接等）本次未全部打开；建议发布前用链接检查工具统一跑一遍。

---

## 开源准备度

**缺失项**（均已 `ls -a` 确认不存在）：

- 无 `package.json`（无 `bin` 映射、无 `engines: node>=22` 声明、无 `scripts`）。
- 无 `.gitignore`（也**不是 git 仓库**——`git rev-parse` 报 `not a git repository`；`~/.claude/api-vault.env`、`~/.config/sph-transcript/.env`、`~/.env` 都在可读列表里，一旦误提交后果严重）。
- 无 `CHANGELOG.md`、无 `CONTRIBUTING.md`、无 `SECURITY.md`、无 `CODE_OF_CONDUCT.md`。
- 无 `.github/workflows/`（无 CI）、无 `examples/`、无测试。
- `LICENSE` 存在（标准 MIT）✓，署名主体为 `Copyright (c) 2026 sph-transcript contributors`（`LICENSE:3`）——对开源可用，但若计划以个人/组织名义发布，建议换成真实署名。

**法律 / 合规风险**：

- 项目核心依赖**腾讯非公开接口**（`src/resolve.mjs:9-11` 自述）：`/api/weixin/get_parse_result` 与 `/api/findergetobjecturl`。README `:186-190` 有"依赖非公开接口""建议用小号"的提示 ✓，**但没有任何"使用者需自行评估合规性 / 可能违反腾讯服务条款 / 可能被视为绕过访问控制"的明确免责**。
- SKILL 的 `cautions`（`SKILL.md:12-14`）同样只提"接口会失效""用小号"，未提合规。
- "不装证书、不改代理"确实降低了技术对抗性（相比 MITM 方案），但**它仍然是自动化调用未对外承诺的私有接口**。建议 README 增加独立的"合规与使用边界"小节，并明确"仅供研究/自有内容处理，不对接口可用性与合规性作任何担保"。
- 另注意 `README.md:190` 提到了版权风险 ✓（这点有，值得肯定）。

**命名**：

- `sph` 对不了解视频号的人是**歧义缩写**（容易被读成"sphere""sphygmo"等）。仓库名 `sph-transcript` 尚可（有 "transcript" 兜底），但 CLI 命令 `sph` 单独看无自解释性。
- 建议：保留 `sph-transcript` 作为包/仓库名，CLI 增加 `transcript` 或 `vcast` 之类的别名，或至少在 `-h` 第一行点明 "sph = 视频号(Shipinhao)"。
- 目前 `README.md:3` 与 `bin/sph.mjs:64` 的标题已含"视频号链接 → 逐字稿"，可接受，但缩写来源从未在文档里解释。

---

## 开源前必做清单（按优先级）

1. **修 S1**：`chrome-cookies.mjs` 临时 cookie 库用 `finally` 删除。
2. **修 S2**：删除/改写 `troubleshooting.md` 的 TLS"无害"表述，并加运行时检测。
3. **修 S3**：二选一——实现本地文件输入，或从 SKILL/README 删除该承诺。
4. **统一"火山 mp4"口径**：把 `SKILL.md:115`、`architecture.md:109`、`README.md:73`、`backends.md:20,147,153` 全部对齐到代码（`acceptsMp4:true`）。
5. **统一"阿里热词"口径**：修 `README.md:123,130`（改为无需预建词表）。
6. **修 M1**（duration 恒为"未知"）、**M2**（本地热词）、**M4**（volcengine-small 死路径）、**M6**（本地 `-m`）。
7. **修 M3**：`looksLikeMediaUrl` 改用 `new URL().hostname` 做后缀精确匹配。
8. **加最小测试**（`node --test`，零依赖）：覆盖 `slugify` / `hostMatches` / `looksLikeMediaUrl` / `extractHotwords` / `buildCorpusText`。
9. **补开源文件**：`.gitignore`（至少忽略 `node_modules/`、`.env`、`~/.config` 相关内容、`transcripts/`）、`package.json`（`engines.node>=22`、`bin`）、`CHANGELOG.md`、`CONTRIBUTING.md`。
10. **初始化 git 并确认首次提交不含任何密钥**；发布前跑一遍链接检查。
11. 删除死代码（L1-L4）与无效条件（L5），统一两处文件名生成逻辑（L8）。
12. 增加"合规与使用边界"小节；为所有性能数字补"单样本"限定语。
13. 修 `docs/backends.md:38` 的死引用。

---

## 做得好的地方（客观陈述，非恭维）

1. **命令注入面为零**：所有外部进程调用（`chrome-cookies.mjs:78-82` 的 `security`、`media.mjs:61` 的 ffmpeg、`local.mjs:108,121` 的 whisper）**全部使用参数数组**而非 shell 字符串，未发现拼接注入。
2. **`slugify` 抗路径穿越**：实测 `../../etc/passwd` → `etc-passwd`、`....//....//x` → `x`，点、斜杠、反斜杠都被替换（`output.mjs:15-17`）。文件名生成安全。
3. **`hostMatches` 是标准的 cookie 作用域匹配**：实测 `.tencent.com` 不会匹配 `evil-tencent.com` 或 `x.tencent.com.evil.com`（`chrome-cookies.mjs:37-43`）。这是本审计里少数**经实测确认正确**的安全边界。
4. **`session.json` 落盘后 `chmod 0600`**（`session.mjs:106`），并对写回值做了控制字符过滤（`session.mjs:73-83`）。
5. **凭据不进日志**：错误信息只回显服务端响应片段（`resolve.mjs:117,132`）与 HTTP 状态，未发现把 cookie 或 API Key 写进日志/输出文件。
6. **研发文档的诚实度高于平均**：`docs/research/asr-accuracy-survey.md` 明确区分"有数据支撑 / 只有官方自评 / 无证据只能推测"三档，并主动承认"没有任何公开数据能裁定候选模型排序"——这份调研本身是可信的，不该被 README 的确定性口吻埋没。
7. **ASR 结果异步轮询都设了 30 分钟上限**（`bailian.mjs:156`、`volcengine.mjs:150,247`），没有无限循环。
8. **README 主动披露数据流向**（`README.md:174-182`）并提示敏感素材走本地，风险沟通意识到位。

---

# 修复记录（2026-09-30，审计后当天）

审计由独立子代理完成，以下为**主线程的核对与修复**。

## 一、误报澄清（1 条）

| 审计项 | 核对结果 |
|---|---|
| "`src/asr/volcengine.mjs` 导入了 `randomUUID` 但未使用" | **误报**。第 29 行导入、第 91 行 `const requestId = randomUUID();` 在用。是我扫描脚本的统计 bug（把 import 行也算作唯一匹配）。 |

## 二、严重问题修复（3 条）

### S1 ✅ 已修 — Chrome cookie 临时库不清理
`src/chrome-cookies.mjs`：改为 `mkdtempSync` + `try/finally` + `rmSync`。
现在无论正常结束还是中途抛错，含用户登录态的副本都会被删除。
> 报告指出"持续残留隐私数据"——**属实**，原实现只有创建没有删除。

### S2 ✅ 已修 — 把关闭 TLS 校验说成"无害"
`docs/troubleshooting.md`：删掉"对本工具无害"的背书，改为**风险提示**。
原文现在明确写出：本工具正带着**登录态 cookie 与音频**通信，跳过证书校验意味着数据可能被中间人读取，且代码本身无防护。
> 这条批评是对的——**为用户的危险配置背书**是文档不该做的事。

### S3 ✅ 已修 — 声称支持本地文件但代码不支持
`bin/sph.mjs`：新增本地文件分支——输入不是 URL 且文件存在时，跳过登录态与解析，直接把文件交给后端。
`src/asr/bailian.mjs`：`file_url` 为 `oss://` 时补上 **`X-DashScope-OssResourceResolve: enable`** 头。
> **修复过程中再次踩到同一个坑**：漏了这个头会报 `FILE_DOWNLOAD_FAILED`，而报错完全不提是缺头
> （`alibaba-bailian-asr` skill 的 `file-upload-temp.md` 早就写了这条）。
> 补上后本地文件模式实测通过：**7.4 秒出稿**。

## 三、文档与代码不一致修复（13 条）

| # | 问题 | 处理 |
|---|---|---|
| 1 | 火山能否吃 mp4 / 要不要 ffmpeg（文档自相矛盾） | ✅ 全部改为"实测能吃 mp4、零额外步骤" |
| 2 | `volcengine-small` 注释称可显式调用，实际被 ORDER 排除后会 exit(5) | ✅ 保留注释但明确"不在推荐列表" |
| 3 | 声称支持本地文件 | ✅ 已实现（见 S3） |
| 4 | 本地 whisper 热词（`--prompt`）未实现 | ✅ 已实现（`src/asr/local.mjs`），标注为"近似，非原生" |
| 5 | 阿里热词"需预建词表"（且与同文件矛盾） | ✅ 改为 `corpus.text`，零配置 |
| 6 | 默认模型名写成不存在的 `qwen3-asr` | ✅ 改为 `qwen3-asr-flash-filetrans` |
| 7 | "解析 3 个接口" | ✅ 改为 2 个（`resolve.mjs` 只发 2 个请求） |
| 8 | 中间文件位置描述错误 | ✅ 改为"系统临时目录，跑完即删" |
| 9 | 让用户找 `[sph]` 前缀日志（代码不输出） | ✅ 改为"步骤编号 ①②③" |
| 10 | "只读一个域名 cookie"（实际含父域） | ✅ 改为"该域名及其父域" |
| 11 | README 选项表漏 `--probe` | ✅ 已补 |
| 12 | `paraformer` 残留注释 | ✅ 已清理 |
| 13 | 后端总表漏本地 whisper | ✅ 已补 |

**另**：SKILL.md 版本号 `0.1.0` → `0.3.0`（与 `bin/sph.mjs` 对齐）；README 耗时表 `0.4+8.7=9.1` 修正（原写 9.4）。

## 四、仍未处理（留待开源前）

- 无测试（`extractHotwords` / `slugify` / `hostMatches` 都可独立测试，值得补）
- 无 `.gitignore`、`CHANGELOG.md`、`CONTRIBUTING.md`、CI
- `docs/backends.md` 引用了仓库外的 `alibaba-bailian-asr` skill 路径（跨仓库死引用）
