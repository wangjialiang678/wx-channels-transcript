# ASR 后端配置指引

本工具支持多个语音识别后端，会自动探测、优先云端。这份文档说清**每个后端怎么配置**。

> 设计原则：**本工具不把注册流程写死在代码里**——那些流程会变，而官方文档链接不会。
> 所以下面每一项都给出**官方文档入口**，你可以（或让你的 Agent）照着官方文档完成注册。

---

## 快速决策

```bash
wx-channels-transcript scan      # 自动探测 + 推荐
wx-channels-transcript backends  # 列出所有后端及各自缺什么
```

| 你的情况 | 建议 |
|---|---|
| 已有阿里云账号 / 百炼 Key | ⭐ **直接用，零额外步骤** |
| 已有火山引擎账号 | 用火山，但需要 ffmpeg 抽音频 |
| 什么都没有，也不想注册 | 装本地 whisper.cpp，完全离线免费 |
| 数据敏感、不能出本机 | 只能走本地后端 |

---

## 1. 阿里云百炼（DashScope）— 推荐

**为什么推荐**：它是唯一**既能接受公网直链、又能直接处理 mp4** 的后端。于是整条链路可以做到「拿到直链 → 直接提交 → 出稿」，**不下载、不转码**。实测 433 秒视频约 9 秒出稿。

### 怎么拿 API Key

1. 打开 **百炼控制台**：https://bailian.console.aliyun.com/
2. 用阿里云账号登录（没有就注册，个人认证即可）
3. 开通「模型服务」中的**语音识别**相关模型
4. 到 **API-KEY 管理**页面创建一个 Key

> 官方文档入口：https://help.aliyun.com/zh/model-studio/getting-started/
> 语音识别模型列表与计费：见 `alibaba-bailian-asr` skill 的 `references/model-selection.md`

### 配置

任选一种（工具会自动读取 `~/.claude/api-vault.env`）：

```bash
# 方式一：写进凭据文件（推荐，长期有效）
echo 'DASHSCOPE_API_KEY=sk-你的key' >> ~/.claude/api-vault.env

# 方式二：临时环境变量
export DASHSCOPE_API_KEY=sk-你的key
```

### 参数

| 项 | 值 |
|---|---|
| 环境变量 | `DASHSCOPE_API_KEY` |
| 模型 | **`qwen3-asr-flash-filetrans`**（默认，也是唯一推荐）<br>`fun-asr` |
| 计费 | 约 ¥0.00008/秒量级。<**以官网为准**> |
| 时长上限 | 12 小时 / 2GB |
| 免费额度 | 新用户通常有，见控制台 |

### ⚠️ 为什么移除了 paraformer

曾经支持过 `paraformer-v2`（更便宜），但实测后**已从代码里移除**：

| 模型 | 专名准确率（**单条样本，见下**） | 架构代际 |
|---|---|---|
| **`qwen3-asr-flash-filetrans`** | **92.9%**（39 对 / 3 错） | 第二代（大模型 ASR） |
| `paraformer-v2` | **10.5%**（4 对 / 34 错） | 第一代（传统 ASR） |

同一段 433 秒中文口播、14 个「英文专名 + 同音字」判据点。

> ⚠️ **这是单条视频、单个测试条件的结果**，只能看量级、不能当基准。测试条件与完整数据见 [`benchmarks.md`](benchmarks.md)。

paraformer 的典型错误：`李飞飞→李飞霏`、`苏姿丰→苏之峰/苏之风/朱之峰`、`AI教母→AI酵母`、`ChatGPT→恰恰的gt`、`MIT→卖毕业`、`CES→四主舞台`、`Atlas→at拉`。

> **不是 paraformer 差**——它是传统 ASR 的巅峰（AISHELL-1 CER 1.95%，比 Whisper 好得多）。
> 但它**没有世界知识**：听清 /sū zī fēng/ 没问题，但不知道"苏姿丰"是个人名。
> **对口播文案来说，专名错了整篇就不能用**，省下的钱不值得。

### 热词：走 `parameters.corpus.text`，**不需要建词表**

阿里侧有两种入口，**都能用，但只有一种适用于异步接口**：

| 接口 | 热词入口 | 实测 |
|---|---|---|
| `qwen3-asr-flash`（同步，≤5 分钟） | `input.messages` 里的 system message | 官方 Demo + Speak Low 项目已验证 |
| **`qwen3-asr-flash-filetrans`（异步，我们在用）** | **`parameters.corpus.text`** | ✅ **实测有效** |

**实测有效的写法**：

```json
{
  "model": "qwen3-asr-flash-filetrans",
  "input": { "file_url": "..." },
  "parameters": {
    "corpus": { "text": "本次录音涉及以下专有名词……\n苏姿丰、李飞飞、World Labs" }
  }
}
```

**实测效果**（同一段 433 秒口播）：

| 专名 | 无热词 | 有 corpus.text |
|---|---|---|
| `苏姿丰` | 8 对 / **2 错** | **14 对 / 0 错** |
| `AI教母` | 0 对 / **1 错**（"AI酵母"） | **1 对 / 0 错** |

> `AI教母` 并不在热词列表里，但也被纠正了——说明 `corpus.text` 不只做词表匹配，
> **还给模型提供了领域上下文**，整体识别倾向都变好。

**`corpus.text` 是高度容错的**（官方原话：「甚至无意义的文本也不会影响识别」），所以可以写得更有信息量。
本工具默认从视频标题自动提取专名填进去。

### ⚠️ 两个实测踩过的坑

| 坑 | 现象 |
|---|---|
| 用 `input.messages`（同步版写法） | **直接报错** `InvalidParameter.MalformedURL` / "A valid file URL is required." —— 异步接口只认 `input.file_url` |
| 用 `parameters.context` | 任务 `SUCCEEDED`，但**热词完全无效**（输出与无热词逐字相同）——典型的"未知参数被静默忽略" |

**教训**：这类接口对**未知参数不报错**。判断某个参数是否生效，**不能看任务成功与否，必须比对实际输出**。

### 预编译热词（`vocabulary_id`）—— 仍支持，但已非首选

需要先在百炼控制台创建词表。相比之下 `corpus.text` 无需任何预配置，所以**默认走 corpus**。
`--vocabulary-id` 参数保留，仅在你已有词表时使用。



### 支持的音频格式

wav / mp3 / m4a / aac / amr / **mp4**（实测可直接传视频文件）等。

---

## 2. 火山引擎（豆包语音）

> ⚠️ **先分清两条互不相通的产品线**（这是最容易踩的坑）：
> - **「豆包语音」控制台** → 真 ASR。用 `X-Api-Key`（新版）或 APPID+Token（旧版）。
> - **「火山方舟」平台** → 多模态 LLM 的"音频理解"。用 `ark-` 开头的 Key。
>
> **`ark-` Key 调不了豆包语音的 ASR**，两者不通用。本项目用的是前者。

火山在本项目里拆成**两个后端**，因为它们的格式支持和鉴权方式都不同：

| 后端 id | 定位 | 能吃 mp4 | 鉴权 | 价格 |
|---|---|---|---|---|
| `volcengine` | 大模型标准版（**推荐**，中文最好） | ❌ | `X-Api-Key` | 2.3 元/小时 |
| `volcengine-small` | 小模型（**唯一官方写明支持 mp4**） | ✅ | APPID+Token+cluster | 1.8 元/小时起 |

两者各有 **20 小时/半年** 免费额度（控制台点「试用」领取）。

**怎么选**：
- 想要**最好的中文识别质量** → `volcengine`（代价：需要 ffmpeg 抽音频）
- 想要**完全不碰 ffmpeg** → `volcengine-small`（代价：用的是 1.0 之前的旧模型）
- **都不如阿里云**（既能吃 mp4、又是新模型）——所以阿里云仍是首选

### 怎么拿 Key

两个后端**在同一个控制台**，但入口不同：

**通用前置**：
1. 注册/登录火山引擎：https://console.volcengine.com/auth/signup
2. **完成实名认证**（未实名无法开通 API 服务）：https://console.volcengine.com/user/realname

**大模型线（`volcengine`）**：
3. 进入新版豆包语音控制台：https://console.volcengine.com/speech/new
4. 「开通管理」里开通「录音文件识别大模型 → 标准版」
5. 「API Key 管理」创建 Key：https://console.volcengine.com/speech/new/setting/apikeys

**小模型线（`volcengine-small`）**：
3. 在**同一个控制台的「创建应用 / 开通服务」入口**开通「录音文件识别（标准版）」
4. 从应用详情里复制**三个**字段：`APP ID`、`Access Token`、`Cluster ID`

> ⚠️ **小模型的凭证不在新版 API Key 页面**——新版 `X-Api-Key` 只能调大模型接口。
> 官方文档原文：小模型请求体里 `app.appid` / `app.token` / `app.cluster` **三个字段全部必填**，
> 且「这些信息可以从控制台创建应用开通服务后获得」。
>
> 官方文档：
> - 大模型标准版 API：https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfilerecognitionstandardversionAPI
> - 小模型标准版 API（写明支持 mp4）：https://www.volcengine.com/docs/DoubaoVoice/AudioFileRecognitionStandardEdition
> - 三档模型对比与格式白名单：https://www.volcengine.com/docs/DoubaoVoice/Accessmust-read

### 参数对照

| 项 | `volcengine`（大模型） | `volcengine-small`（小模型） |
|---|---|---|
| 提交端点 | `/api/v3/auc/bigmodel/submit` | `/api/v1/auc/submit` |
| 查询端点 | `/api/v3/auc/bigmodel/query` | `/api/v1/auc/query` |
| 鉴权头 | `X-Api-Key` 或 `X-Api-App-Key`+`X-Api-Access-Key` | `Authorization: Bearer; <token>`（注意分号） |
| 资源 ID | `volc.seedasr.auc`(2.0) / `volc.bigasr.auc`(1.0) | 由 cluster 决定 |
| 音频格式（**文档口径**） | wav / mp3 / ogg / spx / amr / aac / m4a | wav / ogg / mp3 / mp4 |
| 音频格式（**实测口径**） | **mp4 也可直接跑** ✅ | — |
| 单文件 | <512MB，<5h | <512MB，<5h |
| 并发 | 默认 20 QPS（submit/query 共享） | — |

### 实测状态：✅ 已跑通

**两个反直觉的实测结论，都推翻了此前的判断：**

**① 不需要单独申请 ASR Key —— 语音技术线的 Key 是通用的**

实测：本机已有的 `VOLC_TTS_API_KEY`（语音合成的 Key）**能直接调 ASR**，提交并成功返回结果。
而 `ARK_API_KEY`（方舟）和 `DOUBAO_SEARCH_API_KEY` 都是 `401 Invalid X-Api-Key`。

→ 火山「语音技术」产品线（TTS / ASR 等）**共用一套 Key**。已有任一语音类 Key 就能用。

**② 火山大模型能吃 mp4 —— 官方文档的白名单是错的**

官方 `Accessmust-read` 页面写的格式白名单是 `wav / mp3 / ogg / spx / amr / aac / m4a`，**不含 mp4**。

但实测直接提交 mp4 直链 + `format: "mp4"`：
```
x-api-status-code: 20000000   ✅ 成功
audio_info: {"duration": 433426}    ← 433.4 秒，与视频时长完全吻合
```

服务端显然做了容器兼容（mp4 与 m4a 同属 ISO BMFF，读的是内部音轨）。

→ **所以火山也不需要 ffmpeg**，`needsFfmpeg` 已改为 `false`。

### ⚡ 但与阿里云相比：准确率打平，速度慢 4 倍

同一段 433 秒音频，同一批 14 个「英文专名 + 同音字」判据点：

| 模型 | 专名准确率 | 转写耗时 |
|---|---|---|
| `qwen3-asr-flash-filetrans`（阿里） | 92.9%（39 对 / 3 错） | 明显更快 |
| `volc.seedasr.auc`（火山 2.0） | **93.0%**（40 对 / 3 错） | 约慢 3-4 倍 |
| `paraformer-v2`（阿里） | 10.5%（4 对 / 34 错） | — |

> **别拿 92.9% vs 93.0% 说谁更准**——差 0.1 个百分点，统计上等于平手，而且这是**单条样本**。
> 精确耗时与测试条件见 [`benchmarks.md`](benchmarks.md)。

**准确率几乎完全打平**（差 0.1 个百分点，统计上等于平手），**但火山慢约 4 倍**——符合命名逻辑：阿里那个叫 `flash`，火山标准版文档自称"返回时间：适中，3h 内返回"。

**两家的错误还高度一致**：`苏姿丰` 都被错写成"朱志峰"之类各 2 次。
> 💡 这其实是个**有用的信号**：**两家都错的地方，说明那段音频本身就含糊**（或者发音确实接近），是人工核对的重点区域。

**结论**：**默认仍用阿里云 qwen3**（打平准确率、快 4 倍）。火山作为**交叉验证**备选——某条稿子怀疑有误时换它跑一遍对照。

```bash
wx-channels-transcript "<链接>" -t volcengine          # 用火山跑
wx-channels-transcript "<链接>"                        # 默认（自动选阿里云）
```

### 配置

```bash
# 方式一：已有任一火山语音类 Key（TTS 也行，实测通用）
echo 'VOLC_TTS_API_KEY=你的key' >> ~/.claude/api-vault.env
# 或
echo 'VOLC_ASR_API_KEY=你的key' >> ~/.claude/api-vault.env

# 方式二：旧版控制台的 APP ID + Token
echo 'VOLC_ASR_APP_ID=你的appid' >> ~/.claude/api-vault.env
echo 'VOLC_ASR_ACCESS_TOKEN=你的token' >> ~/.claude/api-vault.env
```



---

## 3. 本地模型（whisper.cpp）

**特点**：完全离线、免费、数据不出本机。
**代价**：需要装推理引擎 + 下模型（几百 MB），推理慢，吃 CPU/内存。

### 怎么装

```bash
# macOS（推荐）
brew install whisper-cpp

# 下载模型（small 约 466MB，中文够用；要更准换 medium 约 1.5GB）
mkdir -p ~/.cache/whisper
curl -L -o ~/.cache/whisper/ggml-small.bin \
  https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin

# 还需要 ffmpeg 抽音频
brew install ffmpeg
```

> 上游项目：https://github.com/ggml-org/whisper.cpp
> 模型列表：https://github.com/ggml-org/whisper.cpp#models

### 配置

工具会自动探测可执行文件和模型；也可以用环境变量指定：

```bash
export WHISPER_MODEL=/path/to/your/ggml-model.bin
```

### 可选的替代引擎

- **sherpa-onnx**（https://github.com/k2-fsa/sherpa-onnx）——同样支持 SenseVoice 等中文优化模型，速度比 whisper 快，**推荐中文场景**。本工具的探测逻辑留了扩展位，需要时可在 `src/asr/local.mjs` 里加。

---

## 还没支持的后端（欢迎补充）

| 后端 | 状态 | 说明 |
|---|---|---|
| 腾讯云 ASR | 未实现 | 接口形态与火山类似（AppId + SecretId/Key），欢迎 PR |
| 讯飞 | 未实现 | 中文效果好，但鉴权是 HMAC 签名，稍复杂 |
| 本地 FunASR / SenseVoice | 未实现 | 中文效果优于 whisper，值得加 |

**加一个后端很简单**：在 `src/asr/` 下新建一个文件，导出 `{ id, label, envKeys, acceptsUrl, acceptsMp4, needsFfmpeg, detect(env), transcribe(opts) }`，然后在 `src/asr/index.mjs` 的 `ORDER` 里注册即可。

---

## 通用：凭据放哪

工具按以下顺序读取（后者覆盖前者），所以你可以沿用自己既有的密钥管理方式：

1. `~/.claude/api-vault.env`
2. `~/.config/sph-transcript/.env`
3. `~/.env`
4. 真实环境变量（**优先级最高**，便于临时覆盖）

文件格式就是普通的 `KEY=value`，支持 `export` 前缀、引号包裹、`#` 注释。
以 `DISABLED` / `TODO` / `your_` 开头的占位值会被自动跳过。
