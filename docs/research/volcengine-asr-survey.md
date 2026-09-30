# 火山引擎语音识别模型调研

> 调研时间：2026-09-30
> 调研目的：为「微信视频号口播视频 → 文字稿」工具挑选第二个 ASR 后端（已接阿里云百炼 `qwen3-asr-flash-filetrans`）
> 调研方法：官方文档真访问（WebFetch）+ 语义检索（exa）+ Google 中文检索（serper）+ 微信生态（cimidata）

**一句话结论**：
火山引擎的 ASR 分两条互不相通的线——**「豆包语音」控制台**（真正的 ASR 产品，用 `X-Api-Key`/APPID+Token 鉴权）和**「火山方舟」**（多模态 LLM 的音频理解，用 `ark-` Key）。**推荐给本项目的是「豆包录音文件识别模型 2.0 标准版」（`volc.seedasr.auc`）**；但它**不接受 mp4**，必须先本地用 ffmpeg 抽出音轨转 mp3/m4a。**`ARK_API_KEY` 不能调用豆包语音的录音文件识别**（官方两套体系，第三方实测亦明确说不行），但可以在方舟平台用 `doubao-seed-2-0-lite` 等多模态模型做"音频理解式转写"。

---

## A. 模型全景对比表

### A.1 「豆包语音」线 —— 真正的 ASR 产品（推荐主用）

| 模型/产品 | resource id / 端点 | 定位 | 价格（后付费标价） | 格式支持 | 时长/大小 | 返回时间 | 来源 |
|---|---|---|---|---|---|---|---|
| **豆包录音文件识别模型 2.0（标准版）** | `volc.seedasr.auc`<br>`/api/v3/auc/bigmodel/submit` + `/query` | 旗舰，异步 submit/query，支持上下文/热词/多模态图片理解 | 2.3 元/小时（标价）；资源包低至 0.7~0.77 元/小时 | wav / mp3 / ogg / spx / amr / aac / **m4a**（API 页只列 raw/wav/mp3/ogg） | <5h，<512MB | 3h 内 | [Accessmust-read](https://www.volcengine.com/docs/DoubaoVoice/Accessmust-read)、[API](https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfilerecognitionstandardversionAPI)、[计费](https://www.volcengine.com/docs/DoubaoVoice/Billinginstructions-21) |
| 豆包录音文件识别模型 1.0（标准版） | `volc.bigasr.auc` | 上一代大模型 | 同上档位 | 同上 | <5h，<512MB | 3h 内 | 同上 |
| **豆包录音文件识别极速版** | `volc.bigasr.auc_turbo`<br>`/api/v3/auc/bigmodel/recognize/flash` | 同步一次返回，无轮询；速度最快 | 4.5 元/小时（标价）；资源包 2.2~4.4 元/小时 | wav / mp3 / ogg / spx / amr / aac / m4a（官方"使用限制"段只写 WAV/MP3/OGG OPUS） | <2h，<100MB | 30min 音频约 10s | [Accessmust-read](https://www.volcengine.com/docs/DoubaoVoice/Accessmust-read)、[极速版API](https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfileLiterecognitionAPI) |
| 豆包录音文件识别闲时版 | `volc.bigasr.auc_idle`<br>`/api/v3/auc/bigmodel/idle/submit` | 闲时算力，最便宜 | 1.2 元/小时（标价）；资源包低至 0.5 元/小时 | wav / mp3 / ogg / spx / amr / aac / m4a | <5h，<512MB | 24h 内 | [Accessmust-read](https://www.volcengine.com/docs/DoubaoVoice/Accessmust-read)、[闲时版API](https://www.volcengine.com/docs/6561/2608618) |
| 大模型流式语音识别 / 豆包流式语音识别模型 2.0 | `volc.seedasr.sauc.duration`（WebSocket） | 实时流式 | 4.5 元/小时 | 实时音频流 | — | 实时 | [计费](https://www.volcengine.com/docs/DoubaoVoice/Billinginstructions-21) |
| 一句话识别 | WebSocket | 短音频（<60s） | 3.5 元/千次起 | 短音频 | <60s | 实时 | [计费](https://www.volcengine.com/docs/DoubaoVoice/Billinginstructions-21) |
| **录音文件识别（标准版）**（小模型，非大模型） | `/api/v1/auc/submit` + `/query`（APPID+Token+cluster） | 老一代小模型 | 1.8 元/小时起；资源包 0.4~1.7 元/小时 | **wav / ogg / mp3 / mp4** ⚠️**官方写明支持 mp4** | <5h，<512MB | 异步 | [标准版(小模型)API](https://www.volcengine.com/docs/DoubaoVoice/AudioFileRecognitionStandardEdition) |
| 录音文件识别（极速版）（小模型） | `/api/v1/auc/submit` | 老一代小模型极速 | 3 元/小时起 | **wav / ogg / mp3 / mp4** ⚠️**官方写明支持 mp4** | <5h，<512MB | 异步 | [极速版(小模型)API](https://www.volcengine.com/docs/6561/192519) |
| 音视频字幕生成 / 自动字幕打轴 | `/api/v1/vc/submit`、`/api/v1/vc/ata/submit` | 字幕专用（含打轴） | 字幕生成 6.5 元/小时起；打轴 5 元/小时起 | 产品页写 WAV/M4A/MP3/MP4/MOV/OGG ≤200M；**但 API 文档要求"客户端抽取视频音轨转成音频文件"** ⚠️口径矛盾 | ≤200M（产品页） | 异步 | [产品概述](https://www.volcengine.com/docs/DoubaoVoice/ProductOverview-8)、[API](https://www.volcengine.com/docs/6561/80909) |
| 豆包语音妙记模型 | 音视频转写+总结 | 会议纪要类 | 1.8 元/小时 + 结构 0.11~0.5 元/小时 | 视频 MP4/AVI/MKV/MOV/FLV/WMV；音频 MP3/WAV/AAC/FLAC/OGG | <1G，≤2h | 异步 | [妙记API](https://www.volcengine.com/docs/DoubaoVoice/DoubaoVoiceMinutes-APIAccessDocumentation) |
| 豆包同声传译大模型 2.0 / 端到端实时语音大模型 / 语音播客大模型 | — | 语音翻译 / 实时对话 / 播客 | 按 token | — | — | 实时 | [计费](https://www.volcengine.com/docs/DoubaoVoice/BillingOverview-15) |

### A.2 「火山方舟」线 —— 音频理解（LLM 路线，非专用 ASR）

| 模型/产品 | model id | 定位 | 计费 | 格式/限制 | 来源 |
|---|---|---|---|---|---|
| 方舟音频理解（全模态模型） | `doubao-seed-2-0-lite-260428`（及 2.0 系列其他） | 多模态 LLM，可直接"语音转写"（提示词驱动） | 按 token | 音频 ≤25MB、≤120min；**只吃 audio，不吃视频**（视频需先抽音轨） | [方舟音频理解](https://docs.volcengine.com/docs/82379/2377589) |
| 方舟模型广场 · 豆包音频 | `doubao-seed-audio-1-0` | 方舟上的音频模型 | — | 【未证实细节】 | [模型详情](https://ark.volcengine.com/region:cn-beijing/model/detail?name=doubao-seed-audio-1-0) |
| 方舟 Agent Plan 语音模型 | `doubao-seed-asr-2.0` → `volc.seedasr.sauc.duration` | **仅流式** WebSocket（`/api/v3/plan/sauc/...`） | 按订阅 | 实时音频流 | [方舟接入语音模型](https://www.volcengine.com/docs/ark/agent-plan-personal-voice-model) |

> 关键结构判断：方舟**没有**与「豆包录音文件识别」对等的"异步长音频文件 ASR 模型"；方舟的音频能力是"多模态理解"（LLM）或"流式 ASR"两条，都不是本项目要的批处理文件转写。

### A.3 版本演进与定位

- **模型 1.0（`volc.bigasr.auc`）**：上一代，官方仍在维护。
- **模型 2.0（`volc.seedasr.auc`）**：2025-12-05 正式发布（[官方公告](https://news.qq.com/rain/a/20251205A054DF00)、[火山引擎公众号](https://mp.weixin.qq.com/s/U3OyDRFXElPbl0EIaBfUGA)）。基于 Seed MoE 架构 + 20 亿参数音频编码器；主打**上下文推理**（关键词召回 +20%）、**多模态视觉输入**（可传 1 张图辅助识别，图 ≤500KB jpeg/jpg/png）、**13 种海外语种**。官方口径："高度保持中、英和方言识别准确度"。
- 官方**没有**发布可横向对比的第三方 ASR 榜单；Seed-ASR 的技术报告口径是"中英文公开测试集上字错误率比此前同代大模型降 10%~40%"，且主观评测在直播/视频/会议场景优于人工转录员（[腾讯新闻转述](https://news.qq.com/rain/a/20240822A086DA00)）。**这是官方自评，非独立第三方**。

---

## B. 格式支持核实（重点）

### B.1 结论：火山到底能不能直接吃 mp4？

**分产品看，结论不统一——这正是文档"分散在多页"造成的坑：**

| 接口 | 能否直接吃 mp4 | 依据强度 |
|---|---|---|
| 大模型录音文件识别（`volc.seedasr.auc` / `volc.bigasr.auc` / `_turbo` / `_idle`） | ❌ **不能** | **官方明确列举**：格式白名单为 `wav/mp3/ogg/spx/amr/aac/m4a`，**无 mp4**（Accessmust-read 对比表）；API 页格式字段写 `raw/wav/mp3/ogg` |
| 小模型录音文件识别（`/api/v1/auc/submit` 标准版 + 极速版） | ✅ **能** | **官方明确列举**：格式字段原文"`wav / ogg / mp3 / mp4`，默认以文件名后缀作为格式" |
| 音视频字幕生成（`/api/v1/vc/submit`） | ⚠️ **矛盾** | 产品概述页写支持 MP4/MOV，**但同产品 API 文档第 1 步写明"客户端抽取视频中音轨，转成音频文件"** → 官方两处口径打架，**未证实** |
| 豆包语音妙记 | ✅ 能（视频 MP4/AVI/MKV/MOV/FLV/WMV） | 官方 API 文档参数表 |
| 方舟音频理解（LLM） | ❌ 不能（只接受 audio） | 官方文档："如需处理视频中内嵌的音频信息，请使用支持音频理解的模型"，示例均为音频文件 |

### B.2 对用户假设的裁定

> 假设："字节的语音模型理论上也应该支持所有阿里云支持的格式（含 mp4）"

**部分推翻，部分成立：**

1. **"字节全线都不支持 mp4" —— 推翻。** 字节**确实有官方写明支持 mp4 的 ASR 接口**，即老一代小模型「录音文件识别标准版/极速版」（`/api/v1/auc/submit`），以及「豆包语音妙记」。
2. **"我们要用的那个大模型接口支持 mp4" —— 不成立。** 本项目要接的 `volc.seedasr.auc`（v3 bigmodel）**明确不支持 mp4**，格式白名单里没有它。
3. 因此"字节理论上应该像阿里一样直接吃 mp4"这个推论**在旗舰大模型线上不成立**。差异来自产品设计：阿里百炼 `qwen3-asr-flash-filetrans` 内置了视频容器解析；火山大模型线只声明音频容器，视频解析交给上层产品（字幕生成/妙记）或客户端。

### B.3 `audio.url` 提交 mp4 链接，服务端会自动抽音吗？

- **官方未承诺。** 三处大模型文档（标准版/极速版/闲时版）都只说"需提供可下载的音频文件地址"，格式字段是**必填白名单**，**没有**任何"传视频链接会自动抽音"的说明。
- 反向证据：错误码 `45000151 音频格式不正确`；Accessmust-read 的格式白名单不含 mp4。
- **结论**：【未证实为支持】，按**不支持**对待是稳妥的。若要验证，只能实测（传 mp4 URL + `format` 试 `mp4` / `m4a`）。
- **一个可尝试的灰色路径（推断，未证实）**：mp4 与 m4a 同属 ISO BMFF 容器，m4a 在官方白名单内。把视频链接以 `format: "m4a"` 提交，**可能**被当作纯音频容器解析成功（因为服务端读的是容器内的 AAC 音轨）。但**无官方背书，风险自负**，建议先实测再依赖。

### B.4 限制与配额

| 项 | 标准版（2.0） | 极速版 | 闲时版 | 小模型标准版/极速版 |
|---|---|---|---|---|
| 单文件大小 | <512MB | <100MB | <512MB | <512MB |
| 单文件时长 | <5h | <2h | <5h | <5h |
| 提交速率 | 半小时内提交总时长 ≤500h | 同左 | 同左 | 半小时内提交总时长 ≤500h |
| 并发/QPS | 默认最大 **20 QPS**；submit 与 query 共享并发 | 独立并发配额，可与标准版分别占额 | 独立并发配额 | 错误码中有 `1003 访问超频` |
| 结果保留 | 24h | — | 24h | 24h |

来源：[Accessmust-read](https://www.volcengine.com/docs/DoubaoVoice/Accessmust-read)、[计费说明](https://www.volcengine.com/docs/DoubaoVoice/Billinginstructions-21)、[标准版API](https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfilerecognitionstandardversionAPI)。

### B.5 免费额度

- **豆包录音文件识别模型 2.0：20 小时 / 半年**（试用额度，控制台点【试用】领取）。
- 大模型录音文件识别（含极速版、闲时版）：各 **20 小时 / 半年**。
- 录音文件识别（小模型，含极速版）：标准版和极速版**各 20 小时 / 半年**。
- 音视频字幕生成 / 自动字幕打轴：各 20 小时 / 半年。
- 来源：[计费概述 BillingOverview-15](https://www.volcengine.com/docs/DoubaoVoice/BillingOverview-15)

---

## C. Key 与开通路径

### C.1 `ARK_API_KEY`（`ark-xxxx`）能不能调 ASR？

**结论：不能调用「豆包语音」的录音文件识别服务；能在「火山方舟」平台用多模态模型做音频转写。**

- **官方层面**：豆包语音所有 ASR 文档的鉴权，要么是 `X-Api-Key`（新版控制台），要么是 `X-Api-App-Key` + `X-Api-Access-Key`（旧版控制台），**从未出现 `ark-` 形式的方舟 Key**，也从未指向方舟控制台。[API 文档](https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfilerecognitionstandardversionAPI)
- **第三方实测（未获官方确认，但表述非常明确）**：SmartSub 接入指南原文——"豆包听写必须使用「豆包语音」控制台签发的 API Key；**火山方舟（Ark，大模型平台）的 API Key 不能用于此服务**"。[SmartSub 文档](https://smartsub.linxiaodong.com/guides/cloud-asr/volcengine)
- **方舟确实有"语音识别"**，但形态不同：
  - **音频理解**（推荐）：`POST https://ark.cn-beijing.volces.com/api/v3/responses`（或 `/chat/completions`），`Authorization: Bearer $ARK_API_KEY`，model 如 `doubao-seed-2-0-lite-260428`，把音频塞进 `input_audio` 让模型"识别音频内容"。**这是 LLM 转写，按 token 计费，非专用 ASR**。[方舟音频理解](https://docs.volcengine.com/docs/82379/2377589)
  - **方舟 Agent Plan 语音模型**：`doubao-seed-asr-2.0`，**仅流式 WebSocket**，用的是"专属 API Key"（非普通 ark- key）。[方舟接入语音模型](https://www.volcengine.com/docs/ark/agent-plan-personal-voice-model)

**给本项目的结论**：把 `ARK_API_KEY` 接到"豆包录音文件识别"上会直接鉴权失败。**要做火山 ASR，必须去「豆包语音」控制台单独拿 Key。**

### C.2 开通前置条件

1. **注册火山引擎账号** → [注册](https://console.volcengine.com/auth/signup)
2. **完成实名认证**（个人/企业均可；API 服务需实名后才能开通）— 来源：[闪电说文档](https://shandianshuo.cn/docs/faq/cloud-speech-model-new)、[快速入门](https://www.volcengine.com/docs/6561/163043)
   - 个人认证即可跑通；未提及强制企业认证。（【未证实】是否对企业认证有额外要求——文档未写）
3. **在「豆包语音」控制台创建应用 / 开通具体服务**（录音文件识别大模型-标准版或极速版）— **未开通时即使 Key 有效也会被拒**。

### C.3 从注册到拿到可用 Key 的完整路径（新版控制台，推荐）

| 步骤 | 操作 | 直达链接 |
|---|---|---|
| 1 | 注册/登录火山引擎 | https://console.volcengine.com/auth/signup |
| 2 | 完成实名认证 | 控制台右上角"账号管理 → 实名认证" https://console.volcengine.com/user/realname |
| 3 | 进入**新版豆包语音控制台** | https://console.volcengine.com/speech/new |
| 4 | **开通管理**中开通「录音文件识别大模型」→ **标准版**（对应 `volc.seedasr.auc`，即模型 2.0） | https://console.volcengine.com/speech/new |
| 5 | **API Key 管理**页创建/复制 API Key | **https://console.volcengine.com/speech/new/setting/apikeys** |
| 6 | 调用（新版只需一个 Key） | 见下方代码 |

```
POST https://openspeech.bytedance.com/api/v3/auc/bigmodel/submit
X-Api-Key:        <新版控制台签发的 API Key>
X-Api-Resource-Id: volc.seedasr.auc      # 模型 2.0；volc.bigasr.auc=模型 1.0
X-Api-Request-Id: <自己生成的 UUID>       # 提交和查询用同一个
X-Api-Sequence:   -1
```

> 旧版控制台鉴权：`X-Api-App-Key`(APP ID) + `X-Api-Access-Key`(Access Token)，无需 X-Api-Key。
> 若走方舟 Agent Plan：专属 Key 在 https://console.volcengine.com/ark 侧。

---

## 选型建议（针对中文短视频口播）

**前提**：微信视频号素材是 mp4，需要"上传即转写、越快越好"。

### 首选：豆包录音文件识别模型 2.0 标准版（`volc.seedasr.auc`）

- **理由**：中文口播场景识别质量第一梯队（官方自评对专有名词/人名/多音字做了专门优化，且支持传图辅助识别，对口播里的品牌名、口播词很友好）；异步 submit/query 与本项目"上传→轮询→出稿"的模型完全一致；价格标价 2.3 元/小时，买资源包可压到 0.7~0.8 元/小时，比 1.0 便宜且有 20 小时免费额度。
- **代价**：**不能直接喂 mp4**。

### 必须做的改造：本地 ffmpeg 抽音轨

由于 `volc.seedasr.auc` 不吃 mp4，且 512MB / 5h 上限对短视频绰绰有余，建议：

```bash
# 抽音轨转 mp3（16k 单声道即可，口播场景够用，体积小、上传快）
ffmpeg -i input.mp4 -vn -acodec libmp3lame -ar 16000 -ac 1 -b:a 64k output.mp3
```

- 这一步是"多一个后端"的隐性成本，需要在适配层做**格式归一化**（阿里云要 mp4 原件、火山要 mp3），否则两个后端无法共用同一份输入。
- 若想省一次转码，可试 `format: "m4a"` 直传（**未证实**，需实测；成功则能省掉重编码）。

### 备选方案

| 方案 | 适用场景 | 代价 |
|---|---|---|
| **极速版 `volc.bigasr.auc_turbo`** | 想要"一次请求即返回"，延迟最低 | 4.5 元/小时（最贵）；<2h、<100MB |
| **闲时版 `volc.bigasr.auc_idle`** | 离线批处理、不急、走量 | 24h 内才返回，不适合交互式出稿 |
| **小模型标准版 `/api/v1/auc/submit`** | **唯一能官方直吃 mp4 的录音识别接口** | 识别质量是 1.0 之前的旧模型，且鉴权走 APPID+Token+cluster 老体系，功能少 |
| **音视频字幕生成** | 想要带时间轴字幕 | 6.5 元/小时（最贵）；且 API 文档要求客户端先抽音轨，等于白搭 |
| **方舟 `doubao-seed-2-0-lite`** | 已有 ark- key、想复用方舟账单 | LLM 按 token 计费，长音频成本不可控；≤25MB/≤120min；需 ffmpeg 抽音；结果稳定性不如专用 ASR |
| **豆包语音妙记** | 顺带要会议纪要/结构化 | 单价更高，属另一产品线 |

### 与阿里云百炼的组合建议

- **分工**：阿里 `qwen3-asr-flash-filetrans` 保留"直吃 mp4、9 秒出稿"的快速路径；火山 `volc.seedasr.auc` 作为**质量对照/兜底**，用于口播里专有名词多、阿里识别不准的片段。
- **统一适配层**：抽象出 `transcribe(media_path) -> text`，内部按后端决定是否 `ffmpeg` 抽音轨，避免把"要不要转码"泄漏到业务层。

---

## 不确定项

1. **大模型接口传 mp4（或改后缀 m4a）是否会成功** —— 官方未证实，需实测。这是唯一可能省掉 ffmpeg 的路径。
2. **音视频字幕生成到底能否直吃 mp4** —— 官方产品页说支持，API 文档说客户端抽音轨，**官方自相矛盾**，需实测或提工单确认。
3. **个人实名认证是否能开通全部录音文件识别服务** —— 文档只写"实名认证"，未区分个人/企业；未证实是否需企业认证。
4. **`format` 字段口径不一致** —— Accessmust-read 表列 `wav/mp3/ogg/spx/amr/aac/m4a`，而 API 文档字段写 `raw/wav/mp3/ogg`（极速版"使用限制"段又只写 WAV/MP3/OGG OPUS）。以哪个为准需实测。
5. **方舟 `doubao-seed-audio-1-0` 的能力边界** —— 模型详情页为 JS 渲染，未能取到正文，未证实其是否可做文件转写。
6. **官方无独立第三方中文 ASR 榜单** —— 质量排序（2.0 > 1.0 > 小模型）来自官方自评与接入方体感，非权威横评。
7. **旧版 vs 新版控制台 Key 的兼容窗口** —— 官方正在迁移，老文档里的 `Authorization: Bearer` 方式在新接口上不可用（第三方实测）。

---

## 检索过程记录

**使用的通道**：`mcp__exa__web_search_exa`、`mcp__serper__google_search`（gl=cn/hl=zh-CN）、`WebFetch`（逐个真访问 volcengine.com/docs 页）、`mcp__cimidata__search_articles_wechat`。
**未使用**：`tavily`、豆包搜索（按要求避开）。
**失败记录**：serper 对带引号的精确短语查询返回 `400 Query pattern not allowed for free accounts`，改用无引号查询后成功。

**"mp4 是否支持"的完整检索证据链**：

1. 初检（exa）搜到 `AudioFileRecognitionStandardEdition` 摘要含 "wav / ogg / mp3 / mp4" → 与用户已知信息矛盾，触发深挖。
2. WebFetch 真访问 4 个格式相关页，逐一读原文表格：
   - [`AudioFileRecognitionStandardEdition`](https://www.volcengine.com/docs/DoubaoVoice/AudioFileRecognitionStandardEdition)（小模型标准版 v1）→ **format 备注原文含 mp4**，端点 `/api/v1/auc/submit`。
   - [`LargemodelrecordingfilerecognitionstandardversionAPI`](https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfilerecognitionstandardversionAPI)（大模型 v3）→ format = `raw / wav / mp3 / ogg`，**无 mp4**。
   - [`LargemodelrecordingfileLiterecognitionAPI`](https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfileLiterecognitionAPI)（极速版）→ 使用限制段 = `WAV / MP3 / OGG OPUS`。
   - [`6561/192519`](https://www.volcengine.com/docs/6561/192519)（小模型极速版）→ format 备注含 **mp4**。
3. 找到权威对比页 [`Accessmust-read`](https://www.volcengine.com/docs/DoubaoVoice/Accessmust-read)（"录音文件识别-接入必读"），其对比表三版本格式列统一为 `wav/mp3/ogg/spx/amr/aac/m4a`，**三处均无 mp4** → 判定大模型线不支持 mp4。
4. 检索 `火山引擎 录音文件识别大模型 mp4 格式 转码 ffmpeg 报错`（serper，无引号）→ 命中错误码页与错误码 `45000151 音频格式不正确`，无任何"支持 mp4"证据。
5. exa 语义检索 `ByteDance/Volcengine 智能字幕 API mp4 200M` → 命中 [`6561/80909`](https://www.volcengine.com/docs/6561/80909) 字幕生成 API，其流程第 1 步原文"**客户端抽取视频中音轨，转成音频文件**"→ 判定字幕产品的 mp4 宣传与 API 实际不符（标矛盾）。
6. WebFetch 方舟 [`音频理解`](https://docs.volcengine.com/docs/82379/2377589) → 输入为 `input_audio`/`audio_url`，官方提示"处理视频中内嵌音频需用支持音频理解的模型"，示例全为音频 → 判定方舟不吃视频。

**"ARK_API_KEY 能否调 ASR"的证据链**：逐一核对豆包语音全部 ASR 文档鉴权字段（只有 `X-Api-Key` / `X-Api-App-Key`+`X-Api-Access-Key`，无 ark-）；再搜接入方文档，SmartSub 明确写"方舟 API Key 不能用于此服务"；同时确认方舟侧确有独立的音频理解（Bearer ark- key）与 Agent Plan 流式 ASR（专属 Key）。

---

## 参考来源

**官方（一手）**

1. 录音文件识别-接入必读（权威对比表）：https://www.volcengine.com/docs/DoubaoVoice/Accessmust-read
2. 大模型录音文件识别标准版 HTTP（v3，`volc.seedasr.auc` / `volc.bigasr.auc`）：https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfilerecognitionstandardversionAPI
3. 录音文件极速版识别 HTTP（`volc.bigasr.auc_turbo`）：https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfileLiterecognitionAPI
4. 录音文件识别闲时版（`volc.bigasr.auc_idle`）：https://www.volcengine.com/docs/6561/2608618
5. 录音文件识别标准版（小模型，**支持 mp4**）：https://www.volcengine.com/docs/DoubaoVoice/AudioFileRecognitionStandardEdition
6. 录音文件识别极速版（小模型，**支持 mp4**）：https://www.volcengine.com/docs/6561/192519
7. 计费说明（价格全表）：https://www.volcengine.com/docs/DoubaoVoice/Billinginstructions-21
8. 计费概述（免费额度）：https://www.volcengine.com/docs/DoubaoVoice/BillingOverview-15
9. 音视频字幕生成-产品概述（格式与 200M）：https://www.volcengine.com/docs/DoubaoVoice/ProductOverview-8
10. 音视频字幕生成 API（客户端抽音轨）：https://www.volcengine.com/docs/6561/80909
11. 豆包语音妙记 API（视频格式）：https://www.volcengine.com/docs/DoubaoVoice/DoubaoVoiceMinutes-APIAccessDocumentation
12. 火山方舟-音频理解：https://docs.volcengine.com/docs/82379/2377589
13. 火山方舟-接入语音模型（Agent Plan）：https://www.volcengine.com/docs/ark/agent-plan-personal-voice-model
14. 快速入门（旧版控制台，开通步骤）：https://www.volcengine.com/docs/6561/163043
15. 新版控制台 API Key 管理：https://console.volcengine.com/speech/new/setting/apikeys
16. 控制台使用 FAQ：https://www.volcengine.com/docs/6561/196768

**第三方（辅助，标注"未获官方确认"）**

17. SmartSub 接入指南（明确"方舟 API Key 不能用于豆包听写"）— 未获官方确认：https://smartsub.linxiaodong.com/guides/cloud-asr/volcengine
18. 博客园 lindexi：2.0 上下文识别实测（确认 1.0=`volc.bigasr.auc`、2.0=`volc.seedasr.auc`）— 未获官方确认：https://www.cnblogs.com/lindexi/p/19846383
19. AX0X 豆包 STT 接入手册（8 个实战坑，鉴权与轮询细节）— 未获官方确认：https://blog.ax0x.ai/doubao-stt-runbook-zh
20. 闪电说：新版控制台开通流式识别步骤 — 未获官方确认：https://shandianshuo.cn/docs/faq/cloud-speech-model-new
21. 豆包语音识别模型 2.0 发布公告（官方通稿转载）：https://news.qq.com/rain/a/20251205A054DF00
22. 火山引擎开发者社区：Doubao-Seed-2.0-lite 全模态（音频）升级：https://developer.volcengine.com/articles/7636596381943070763
