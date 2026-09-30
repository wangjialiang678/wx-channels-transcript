# 中文 ASR 模型识别率调研

> 调研范围：中文普通话口播场景（单人、清晰录音、含英文专名与数字）下，各主流 ASR 后端的识别准确率证据。
> 调研时间：2026-09-30。所有数字均标注测试集与来源性质。

**一句话结论**：在"中文口播"这个场景里，**有数据支撑的结论只有一条 —— 国产中文 ASR（阿里系 / 火山水系 / 小红书 FireRed）都明显优于 Whisper**（多个独立测试集上 CER 差距 2～4 倍）；而**候选模型中 qwen3-asr-flash-filetrans vs volc.seedasr.auc vs paraformer-v2 三者谁更强，没有任何公开的第三方横评数据可依据**，现有唯一可用证据是两家友商（阿里 Qwen、小红书 FireRed）各自跑的对比表，且两家结论互相矛盾。所以默认模型的排序目前**无法用公开数据裁定**，只能靠自测。

---

## 0. 前置说明：读这份报告前必须知道的四件事

1. **中文用 CER（字错误率），英文用 WER（词错误率），两者不可混比。** 中文没有词边界，用 WER 衡量会因分词规则不同而剧烈波动。任何"中文 WER 3.x%"的数字（如腾讯混元 Hy ASR 的口径）都与"CER 3.x%"不是一回事，本报告统一按来源口径标注。
2. **不同测试集的数字不可直接比较。** AISHELL-1（朗读、安静）上 CER 常常 <2%，WenetSpeech meeting（会议、远场）经常 >10%，同一个模型在两个集上差 5 倍以上。凡跨行比较，必须同测试集。
3. **"厂商自评"要单列。** 阿里的 Qwen3-ASR 报告、字节的火山文档、小红书的 FireRedASR2S 报告，都是厂商自己跑的（其中 FireRedASR2S 还直接在表里踩了豆包和 Qwen 的结果）。它们**方法学上可复现、口径统一**，比纯宣传可信，但**仍非中立第三方**。
4. **产品级 ≠ API 级。** 网上大量"豆包 vs 千问语音输入法"横评测的是**输入法 App**（含 LLM 润色、自动纠错），不是我们候选清单里的**录音文件识别 API**。这类结果对 API 选型只有间接参考价值。

---

## 1. 官方公布的指标（附测试集）

### 1.1 Qwen3-ASR（阿里，含候选 `qwen3-asr-flash-filetrans` 所属家族）

来源：**Qwen3-ASR Technical Report**，arXiv 2601.21337（2026-01），阿里官方。表中含 Qwen3-ASR-0.6B/1.7B（开源模型），并列出阿里**自己跑**的 GPT-4o-Transcribe / Gemini-2.5-Pro / **Doubao-ASR** / Whisper-large-v3 结果。中文列报 CER。

| 测试集 | Qwen3-ASR-1.7B | Qwen3-ASR-0.6B | Doubao-ASR | Whisper-large-v3 | GPT-4o-Transcribe | Gemini-2.5-Pro |
|---|---|---|---|---|---|---|
| WenetSpeech net | **4.97** | 5.97 | N/A | 9.86 | 15.30 | 14.43 |
| WenetSpeech meeting | **5.88** | 6.88 | N/A | 19.11 | 32.27 | 13.47 |
| AISHELL-2 test | **2.71** | 3.15 | 2.85 | 5.06 | 4.24 | 11.62 |
| SpeechIO | **2.88** | 3.44 | 2.93 | 7.56 | 12.86 | 5.30 |
| Fleurs-zh | **2.41** | 2.88 | 2.69 | 4.09 | 2.44 | 2.71 |
| CommonVoice-zh | **5.35** | 6.89 | 5.95 | 12.91 | 6.32 | 7.70 |
| （内部）ExtremeNoise | **16.17** | 17.88 | 17.04 | 63.17 | 36.11 | 29.06 |
| （内部）Dialog-Mandarin | **6.54** | 7.06 | 6.61 | 14.01 | 20.73 | 12.50 |
| （内部）TongueTwister | **2.44** | 4.06 | 3.47 | 16.63 | 20.87 | 4.97 |

- 来源性质：**官方自评**（阿里），但方法学明确："commercial APIs 与 Whisper-large-v3 的结果由我们自己跑测试集得到"。
- ⚠️ `qwen3-asr-flash` / `-filetrans` 是 API，报告里对应的 API 版本写作 "Qwen3-ASR-Flash-1208"（附录 A.1，仅供参考）。开源 0.6B/1.7B 与 API 不是同一个权重，不能把上表数字当 API 数字用。
- 链接：https://arxiv.org/html/2601.21337 ；https://github.com/QwenLM/Qwen3-ASR

### 1.2 FireRedASR2S（小红书，含对 Doubao-ASR / Qwen3-ASR 的对比）

来源：FireRedASR2S 技术报告（arXiv 2603.10420）+ GitHub README，2026。CER，越低越好。

| 测试集 | FireRedASR2-LLM | FireRedASR2-AED | Doubao-ASR | Qwen3-ASR-1.7B | Fun-ASR |
|---|---|---|---|---|---|
| **普通话 4 集平均** | **2.89** | **3.05** | 3.69 | 3.76 | 4.16 |
| AISHELL-1 | 0.64 | 0.57 | 1.52 | 1.48 | 1.64 |
| AISHELL-2 | 2.15 | 2.51 | 2.77 | 2.71 | 2.38 |
| WenetSpeech net | 4.44 | 4.57 | 5.73 | 4.97 | 6.85 |
| WenetSpeech meeting | 4.32 | 4.53 | 4.74 | 5.88 | 5.78 |
| 歌词 opencpop | 1.12 | 1.17 | 4.36 | 2.57 | 3.05 |
| **方言 19 集平均** | **11.55** | **11.67** | 15.39 | 11.85 | 12.76 |

- 来源性质：**官方自评**（小红书），但是**竞品对照表**——它主动跑了豆包与 Qwen，且是同测试集、同口径。
- ⚠️ **与 Qwen 报告互相矛盾**：Qwen 报告里 AISHELL-2 上 Qwen3-1.7B(2.71) 优于 Doubao(2.85)、WS-net 上 Qwen(4.97) 也优于 Doubao(5.73)；FireRed 报告里普通平均却是 Doubao(3.69) 优于 Qwen(3.76)，且 WS-meeting 上 Doubao(4.74) 优于 Qwen(5.88)。**两家友商谁也没把对方/豆包测成全面领先**，说明这种"对手跑的表"存在测试配置差异（VAD、标点、ITN、是否分句）。
- 链接：https://github.com/FireRedTeam/FireRedASR2S ；https://huggingface.co/papers/2603.10420

### 1.3 火山引擎豆包录音文件识别（候选 `volc.seedasr.auc` / `volc.bigasr.auc` / 小模型）

**关键发现：火山从未公布过任何绝对 CER/WER 数字，也没有声明测试集。** 全部为相对幅度声明：

| 声明 | 数值 | 测试集 | 来源性质 | 链接 |
|---|---|---|---|---|
| 相比传统模型识别错误率降低 | 30% | 未公布 | 官方宣传 | [product/asr](https://www.volcengine.com/product/asr) |
| 音乐/科技/教育/医疗等垂直领域错误率降低 | 50% 以上 | 未公布 | 官方宣传 | [ProductOverview-6](https://www.volcengine.com/docs/DoubaoVoice/ProductOverview-6) |
| 口音错误率降低 | 60% | 未公布 | 官方宣传 | 同上 |
| 噪声与背景人声错误率降低 | 30%–50% | 未公布 | 官方宣传 | 同上 |
| 豆包语音识别 2.0 上下文整体关键词召回率提升 | 20% | 未公布 | 官方发布稿（2025-12-05） | [微信发布稿](https://mp.weixin.qq.com/s/U3OyDRFXElPbl0EIaBfUGA) |
| 加热词可提升热词召回率（绝对值） | 5 个点以上 | 未公布 | 官方文档 FAQ | [docs/6561/155743](https://docs.volcengine.com/docs/6561/155743) |

- 2.0（`volc.seedasr.auc`）与 1.0（`volc.bigasr.auc`）的差异，官方只描述**能力项**（2.0 支持上下文/图片、POI、音乐等 function call），**没有任何 1.0→2.0 的准确率对照数字**。
- 第三方软文（掘金，2026-08）称 2.0"公开测试集识别错误率比国内同类模型最高降低 40%"——**无测试集、无方法、属推广文，不可引用**。
- 链接：https://docs.volcengine.com/docs/DoubaoVoice/Accessmust-read

### 1.4 OpenAI Whisper large-v3

- **官方论文**（Radford et al., 2022, arXiv 2212.04356）：主表是英文；明确把中文（ZH）列为"表现低于 trendline 的 outlier"，归因于文字系统差异。Fleurs 中文只在附录给 WER/CER，论文自身承认中文是弱项。
- **官方模型卡**（HuggingFace `openai/whisper-large-v3`）：只报 **LibriSpeech test-clean，英文**，无中文数字。
- **官方 large-v3 说明**：Common Voice 15 + Fleurs，中文用 CER（斜体标注），但官方图只给"相对 large-v2 降低 10–20%"的范围，**无逐语言绝对数值**。
- 结论：**Whisper 官方从未给过一个可直接引用的中文 CER 绝对值。**

### 1.5 SenseVoice / Paraformer（阿里 FunAudioLLM，本地开源候选）

来源：**FunAudioLLM 技术报告** arXiv 2407.04051（2024-07），官方；含 Whisper 对比，中文列报 CER。

| 测试集 | Whisper-S | Whisper-L-V3 | SenseVoice-S | SenseVoice-L | Paraformer-zh |
|---|---|---|---|---|---|
| AISHELL-1 test | 10.04 | 5.14 | 2.96 | 2.09 | **1.95** |
| AISHELL-2 test_ios | 8.78 | 4.96 | 3.80 | 3.04 | **2.85** |
| WenetSpeech meeting | 25.62 | 18.87 | 7.44 | **6.73** | 6.97 |
| WenetSpeech net | 16.66 | 10.48 | 7.84 | **6.01** | 6.74 |
| CommonVoice zh-CN | 19.60 | 12.55 | 10.78 | **7.68** | 10.30 |

推理效率（同报告，A800，batch=1）：

| 模型 | 参数 | RTF | 10s 音频延迟 |
|---|---|---|---|
| Whisper-L-V3 | 1550M | 0.111 | 1281 ms |
| Whisper-S | 224M | 0.042 | 518 ms |
| Paraformer-zh | 220M | **0.009** | **100 ms** |
| SenseVoice-S | 234M | **0.007** | **70 ms** |
| SenseVoice-L | 1587M | 0.110 | 1623 ms |

- 来源性质：**官方自评**（阿里），同报告同口径，是本地开源候选最权威的一组数据。
- 链接：http://arxiv.org/abs/2407.04051 ；https://github.com/FunAudioLLM/SenseVoice

### 1.6 补充参照：腾讯混元 Hy ASR 3.0 preview（非候选，但属国产云端新模型）

- 官方口径：开源评测集上**中文普通话 WER 3.34%**、英语 WER 2.62%、粤语 WER 3.12%（2026-08 发布）。
- ⚠️ 注意这是 **WER** 不是 CER，且"开源评测集"未指明具体集名，**不可与上表的 CER 直接比较**。
- 链接：https://www.qbitai.com/2026/08/465973.html

---

## 2. 独立横评（样本量、测试集、可信度）

### 2.1 ★ SpeechIO Leaderboard —— 最接近"中立第三方"的中文 ASR 榜单【推荐】

- 出品：SpeechColab / WeNet 团队（张彬彬等），非任何厂商。
- 测试集：**37+ 个精心策划的真实场景中文测试集**（`SPEECHIO_ASR_ZH00000`–`ZH00046`），约 **43,000 条 / 66 小时**，专业标注，覆盖新闻、访谈、讲座、**播客、短视频、直播带货、脱口秀、相声、会议、电话、医美、芯片/IT、方言电影**等。样本量充足、场景与"口播视频"高度重叠。
- 最新公开结果（2025-01，全部测试集 ZH00001–46 汇总，CER）：

| 排名 | 厂商/模型 | CER |
|---|---|---|
| 1 | microsoft_batch_zh（微软） | 2.99% |
| 2 | ximalaya_api_zh（喜马拉雅） | 3.35% |
| 3 | aliyun_ftasr_api_zh（阿里） | 3.40% |
| 4 | tencent_api_zh（腾讯） | 4.64% |
| 5 | iflytek_lfasr_api_zh（讯飞转写） | 4.80% |
| 6 | aispeech_api_zh（思必驰） | 5.75% |
| 7 | baidu_pro_api_zh（百度） | 10.10% |

- 与口播强相关的子测试集表现（bilibili_api_zh 参考）：短视频 ZH00013/14/21–24 → 1.69%–4.70%；播客 ZH00009/10 → 3.18%/3.48%；脱口秀 ZH00017 → 2.82%；讲座 ZH00004 → 1.59%；直播带货 ZH00031 → 3.74%；**芯片 ZH00033 → 2.45%；网络 IT ZH00034 → 5.10%**（这两项含大量英文专名与术语，是我们场景最接近的代理指标）。
- ⚠️ **关键缺口**：榜单为 **2025-01 快照**，**没有火山豆包 / Qwen3-ASR-Flash / 豆包 2.0 的条目**（字节未提交）。因此它**不能直接裁定我们候选模型的排序**，只能证明"主流国产云 API 在口播类场景的 CER 大致在 3%–5% 量级，且都远好于 Whisper"。
- ⚠️ 榜单自身声明：头部厂商差距在 0.5% 以内时**不认为有实质差别**（因为无法完全对齐各家 VAD/ITN/语气词处理）。
- 链接：https://github.com/SpeechColab/Leaderboard/

### 2.2 FunASR 官方博客（厂商推广性质，但样本量明确）

- 测试集：**184 个中文长音频文件，合计 11,539 秒（≈192 分钟）**，NVIDIA H100，去标点 CER。
- 结果：SenseVoice-Small **7.81%**、Paraformer-Large 10.18%、Fun-ASR-Nano 8.20%、Whisper-large-v3 **20.02%**、Whisper-large-v3-turbo 21.71%。
- 速度：SenseVoice-Small 169.6× 实时，Whisper-large-v3 仅 13.4×。
- 来源性质：**厂商（FunASR/阿里）推广博客**，测试集自建未公开。可信度低于 SpeechIO，但样本量与结论方向（Whisper 中文 CER 约为国产模型 2–2.6 倍）与 SpeechIO、SenseVoice 论文一致，可作交叉印证。
- 链接：https://www.funasr.com/blog/funasr-vs-whisper-benchmark.html

### 2.3 HuggingFace Open ASR Leaderboard —— 对中文**不可用**

- 86 个模型、12 个数据集，含英文短/长语音与多语言短语音。
- ⚠️ **多语言轨只覆盖 德/法/意/西/葡（DE/FR/IT/ES/PT），没有中文短语音赛道**；长语音是英文 earnings/AMI。
- 唯一涉及中文的是 Qwen3-ASR-1.7B 在该多语言轨的平均 WER **5.11**（排名约第 12）——**与中文 CER 无关**。
- 结论：**不能用它给中文口播选型**。
- 链接：https://huggingface.co/spaces/hf-audio/open_asr_leaderboard ；论文 arXiv 2510.06961

### 2.4 云端 API 实测（第三方，但多为小样本 / 非中文专门）

| 来源 | 测试集/样本 | 结论 | 可信度 |
|---|---|---|---|
| 恆遠 2026-04（繁中） | 200+ 小时，台普 Podcast 10 段 | Chirp3 中文安静 WER 8.5% < Deepgram 10.3% < Whisper-V3 12.1%；噪声场景 Whisper 18.6% | 中（未含国产模型，方言/台湾腔） |
| sipsip.ai 2026-04 | 100 个音频文件（英文为主） | Whisper API Podcast 4.1% / Meeting 9.3% | 中（英文，非中文） |
| Vocova 2026-07 | **FLEURS 每语种仅 50 条**（mlx-whisper） | 提供逐条 WER/CER，中文用 CER | 【样本量小，仅供参考】 |
| 51CTO / CSDN 若干"Whisper vs WeNet vs Paraformer" | 8 段 ~15 分钟 | Whisper-medium 中文 WER 8.7% 等 | 【样本量小，来源可疑，建议忽略】 |

### 2.5 语音输入法横评（**产品级**，非 API）

- 机智猫、雷科技、掘金等多篇"豆包输入法 vs 千问 CosyVoice"实测：仅 **3 个场景（普通话 40s / 粤语 10s / 英文播客）**，结论多为"豆包略胜"。
- ⚠️ 标注：**【样本量小，仅供参考】**，且测的是**输入法产品**（带 LLM 润色/自动纠错的实时转写），**不能外推到录音文件识别 API**。

### 2.6 热词/专名专项横评（CSDN，待验证）

- 一篇 CSDN 博客声称在 **AISHELL-1（2142 条，其中 1000 条含热词）** 上实测：FunASR-Nano 基础 CER 4.17%、热词准确率 70.52%；加 Prompt+Sherpa 组合热词策略后 CER **2.45%**、热词准确率 **88.49%**。GLM-ASR、SenseVoice 同测。
- ⚠️ 标注：**来源为 CSDN 个人博客、方法描述含糊、有 AI 生成痕迹，可信度中低，仅作方向参考，不要引用具体数字。**
- 链接：https://blog.csdn.net/m0_62603533/article/details/160599189

---

## 3. 英文专名与数字的识别能力

这是中文口播最容易翻车的地方，但**公开资料中没有任何"多模型 × 英文专名/数字"的同测试集横评**。以下是能查到的间接证据：

### 3.1 各家的专名/数字相关能力（多为官方自评）

| 模型 | 机制 | 官方声明效果 | 来源性质 |
|---|---|---|---|
| Qwen3-ASR-Flash | 任意格式**背景文本/热词**上下文定制；对无关上下文鲁棒 | "智能利用上下文识别并匹配命名实体与其他关键术语"（无数值）；`enable_itn` 数字规整 | 官方（无数值） |
| 火山豆包 2.0 | PPO 强化学习做上下文推理 | **上下文整体关键词召回率 +20%**；热词召回率绝对 +5 点以上 | 官方（相对值，无测试集） |
| 火山豆包（通用） | 热词纠错（平台级/请求级）、正则替换词、ITN 数字规整 | 未给数值 | 官方 |
| Paraformer / SeACo-Paraformer | 神经网络热词（SeACo + ASF） | AISHELL-1-NER 上低召回热词召回率 **3% → 69% → 79% → 87%** | **官方（阿里云开发者文章，有测试集与复现开源集）** |

- SeACo-Paraformer 的热词数字是目前**最可复现、最可信**的一条专名能力证据：官方开源了 AISHELL-1-NER 热词测试集。链接：https://developer.aliyun.com/article/1587443
- 火山、Qwen 的热词/上下文效果**均只有相对比例、无测试集**，属"⚠️ 只有官方自评"。

### 3.2 间接代理指标

- SpeechIO 的 **ZH00033（芯片，2.45%）、ZH00034（网络 IT，5.10%）** 两个子集是我们能拿到的最接近的"含英文专名中文口播"的公开数据点，但它**只公布了部分厂商的汇总**，没有逐模型 × 逐专名的对照。
- Qwen3-ASR 报告的内部集 **TongueTwister**（绕口令，考多音字/易混词）：Qwen3-1.7B 2.44% vs Doubao-ASR 3.47% vs Whisper 16.63% —— 只能说明"豆包/Qwen 在难词上都远好于 Whisper"，**不等于专名能力排序**。

### 3.3 结论

**没有任何公开资料能够回答"这些模型在中文口播里的英文专名和数字谁更准"。** 这是本次调研最大的证据空白。唯一具备可复现测试集的热词能力证据是 Paraformer 系（可本地开源复现），云端 API 的热词效果全部只能听厂商宣传。

---

## 4. 综合排序（严格区分证据强度）

### ✅ 有数据支撑的结论

1. **国产中文 ASR ≫ Whisper（large-v3 / turbo），且差距很大。** 三条独立来源一致：SenseVoice 论文（AISHELL-1: 1.95–2.96% vs Whisper-L 5.14%；WenetSpeech meeting: 6.7–7.4% vs 18.87%）、Qwen3-ASR 报告（WS-net 4.97% vs 9.86%；WS-meeting 5.88% vs 19.11%）、FunASR 博客（7.81% vs 20.02%）。**Whisper 不应作为中文口播的默认后端。**
2. **SpeechIO 独立榜单显示：主流国产云 API 在真实口播类场景（短视频/播客/脱口秀/讲座）的 CER 约在 1.7%–4.7% 区间，头部厂商之间差距常小于 0.5%（不构成实质差别）。** 阿里（aliyun_ftasr）在该榜单位居前三（全量 3.40%）。
3. **SenseVoice-Small / Paraformer 在中文上远超 Whisper，且 SenseVoice-Small 推理速度约 15× Whisper-Large（RTF 0.007 vs 0.111，官方论文同口径）。** 本地部署选型上，SenseVoice-Small 是"中文准 + 极快"的明确组合；Paraformer-zh 在 AISHELL-1（1.95%）上最优。
4. **Paraformer 系的热词（专名）增强有可复现数据**：AISHELL-1-NER 上低召回热词召回率 3%→87%（官方，开源测试集）。

### ⚠️ 只有官方自评 / 宣传，无独立验证

5. **豆包 2.0（`volc.seedasr.auc`）相比 1.0 全面提升** —— 官方只给能力项差异，**零准确率数字**，纯宣传。
6. **豆包录制文件识别的绝对准确率** —— 只有"比传统模型降 30%/垂直领域降 50%"这类相对宣称，无测试集。
7. **Qwen3-ASR-Flash / FireRedASR2 的榜单第一** —— 均为厂商自跑，且互有矛盾，属自评。
8. **云端 API 的热词/上下文对专名的提升幅度** —— 火山"关键词召回 +20%"、Qwen"上下文定制"均无测试集。

### ❓ 无证据，只能推测

9. **`qwen3-asr-flash-filetrans` vs `volc.seedasr.auc` vs `paraformer-v2` 三者的中文口播排序** —— 没有任何同测试集对照。Qwen 报告暗示 Qwen3-ASR 系 ≈ 或略优于豆包，FireRed 报告暗示豆包 ≈ 或略优于 Qwen3-ASR-1.7B，两者都无法裁定；`paraformer-v2`（百炼 API 版，非开源 Paraformer-large）**连自评数据都没有**。
10. **火山小模型（`/api/v1/auc`，老一代）的水平** —— 官方已将其归入"传统模型"，暗示弱于大模型版，但**无任何公开数字**。
11. **各模型在"英文专名（AMD/ChatGPT/ImageNet）+ 数字（82亿/2014年）"上的准确率** —— 完全无公开对照。
12. **Whisper 的中文绝对 CER** —— 官方从未公布，第三方数字因文本归一化口径不同而不可比（如 large-v3 在 AISHELL-1 上，个人博客报 8.085%，SenseVoice 论文口径报 5.14%）。

### 若要给候选模型一个"证据加权"的暂定排序（⚠️ 含推测成分）

在同一"中文普通话 + 大模型云 API + 有第三方/竞品对照数据"的加权下，**可谨慎推测**（非实证）：

```
第一梯队（无明显优劣差异，CER 量级 3%–4%):  qwen3-asr-flash-filetrans ≈ volc.seedasr.auc
第二梯队:                                        volc.bigasr.auc（1.0，预期略弱，无数据）
第三梯队:                                        paraformer-v2（更便宜，预期略弱，无数据）
本地（如走本地）:                                SenseVoice-Small（准+极快）> Paraformer-large（AISHELL 最优）≫ faster-whisper large-v3
```

**这个排序的证据强度是❓，不应写进产品文档当成结论。** 唯一可靠的做法是**用你们自己的口播样本做 A/B 自测**（见第 5 节）。

---

## 5. 不确定项（需要自测才能定论）

1. **候选三个云端 API 在你们真实素材上的排序。** 公开数据无法裁定。建议自测方案：取 **≥30 条**真实微信视频号口播（覆盖不同主播、含专名与数字），人工转写为 gold，统一文本归一化（去标点、ITN 对齐、简繁统一）后算 CER，且**每个模型都开启各自的热词/上下文功能各测一遍**。
2. **专名与数字的单独命中率。** 建议在自测集里单独统计"英文专名 token 命中率"与"数字 token 命中率"两项，这比整体 CER 更能反映口播场景的可用性。
3. **火山豆包 2.0 的真实提升。** 目前只有"能力项"差异，无任何数字；1.0→2.0 的准确率差需要自测。
4. **SpeechIO 榜单缺豆包/Qwen3-ASR 条目。** 无法用它裁定候选排序；如字节/阿里后续提交，应重新评估。
5. **`paraformer-v2`（百炼 API）与开源 Paraformer-large 是否同一模型。** 未查到说明，不可用开源 Paraformer 的 AISHELL-1 1.95% 替代其 API 表现。
6. **热词功能对"英文专名"是否真有效。** 现有热词数据全部来自中文词（SeACo）或口径不明的博客，**英文专名的热词/上下文增强效果无公开验证**。

---

## 6. 参考来源

**官方技术报告 / 论文**
1. Qwen3-ASR Technical Report — arXiv 2601.21337：https://arxiv.org/html/2601.21337 ｜ https://github.com/QwenLM/Qwen3-ASR
2. FireRedASR2S（含 Doubao-ASR / Qwen3-ASR 对比表）— https://github.com/FireRedTeam/FireRedASR2S ｜ https://huggingface.co/papers/2603.10420
3. FunAudioLLM / SenseVoice / Paraformer — arXiv 2407.04051：http://arxiv.org/abs/2407.04051 ｜ https://github.com/FunAudioLLM/SenseVoice
4. Whisper 论文 — arXiv 2212.04356：https://cdn.openai.com/papers/whisper.pdf
5. Whisper large-v3 官方说明 — https://github.com/openai/whisper/discussions/1762
6. Open ASR Leaderboard 论文 — arXiv 2510.06961：https://arxiv.org/pdf/2510.06961

**独立第三方榜单 / 横评**
7. SpeechIO Leaderboard（SpeechColab/WeNet，最接近中立）— https://github.com/SpeechColab/Leaderboard/
8. HuggingFace Open ASR Leaderboard — https://huggingface.co/spaces/hf-audio/open_asr_leaderboard
9. FunASR vs Whisper 实测（厂商博客，184 文件 / 11539 秒）— https://www.funasr.com/blog/funasr-vs-whisper-benchmark.html
10. Whisper 中文 benchmark 一览（个人博客）— https://zhaoshuaijiang.com/2023/10/20/asr-whisper/

**厂商官方文档 / 发布稿**
11. 火山引擎语音识别产品页：https://www.volcengine.com/product/asr
12. 火山豆包语音产品简介（错误率降低 30%/50%/60%）：https://www.volcengine.com/docs/DoubaoVoice/ProductOverview-6
13. 火山录音文件识别接入说明（1.0/2.0/极速版/闲时版）：https://docs.volcengine.com/docs/DoubaoVoice/Accessmust-read
14. 火山热词 FAQ（+5 点）：https://docs.volcengine.com/docs/6561/155743
15. 豆包语音识别 2.0 发布稿（关键词召回 +20%）：https://mp.weixin.qq.com/s/U3OyDRFXElPbl0EIaBfUGA
16. Qwen3-ASR-Flash 发布稿：https://mp.weixin.qq.com/s/SPL5Aiu6afEXK3-nEwy6_Q
17. 阿里百炼 qwen3-asr-flash 模型信息：https://help.aliyun.com/zh/model-studio/qwen3-asr-flash
18. 阿里百炼 ASR 模型总览（含 qwen3-asr-flash-filetrans）：https://docs.modelstudio.console.alibabacloud.com/zh/model-studio/asr-model
19. Paraformer / SeACo-Paraformer 热词（AISHELL-1-NER 3%→87%）：https://developer.aliyun.com/article/1587443
20. 腾讯混元 Hy ASR 3.0 preview（中文 WER 3.34%）：https://www.qbitai.com/2026/08/465973.html

**需降级标注的（小样本 / 来源可疑）**
21. Vocova FLEURS Whisper 评测（每语种仅 50 条）：https://vocova.app/data/wer-benchmark-2026/README.md
22. CSDN 热词横评（来源可疑）：https://blog.csdn.net/m0_62603533/article/details/160599189
23. 语音输入法横评（产品级，3 场景）：https://mp.ofweek.com/Internet/a556714443687 ｜ https://www.163.com/dy/article/L1NPA2DJ05118UEG.html
24. 腾讯云开发者社区《中文语音识别该用谁》（转录 FireRedASR2S 表）：https://cloud.tencent.com/developer/article/2642961
