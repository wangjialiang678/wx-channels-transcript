/**
 * ASR 后端：阿里云百炼（DashScope）录音文件识别。
 *
 * 为什么把它排在推荐第一位：
 *   · **能直接吃 mp4** —— 于是整条链路可以「拿到直链 → 提交 → 出稿」，
 *     既不用下载到本地，也不用 ffmpeg 抽音频
 *   · 支持公网 URL 直传，和视频号直链天然契合
 *   · 中文识别质量好，专名比通用模型准
 *
 * 接口形态：异步提交 + 轮询（同步接口有 5 分钟 / 10MB 上限）。
 * ⚠️ 两个模型的请求体结构不一样，别混：
 *     qwen3-asr-flash-filetrans      → input.file_url（单对象），结果 output.result.transcription_url
 *     paraformer-v2 / fun-asr 等     → input.file_urls（数组），结果 output.results[].transcription_url
 */

const ENDPOINT = 'https://dashscope.aliyuncs.com/api/v1/services/audio/asr/transcription';
const TASK_URL = 'https://dashscope.aliyuncs.com/api/v1/tasks';
const UPLOAD_POLICY = 'https://dashscope.aliyuncs.com/api/v1/uploads';

/** 单对象结构的模型（用 file_url 而非 file_urls）。 */
const SINGLE_OBJECT_MODELS = /^qwen3-asr-flash-filetrans$/;

export default {
  id: 'bailian',
  label: '阿里云百炼（DashScope）',
  envKeys: ['DASHSCOPE_API_KEY', 'DASHSCOPE_API_KEY_ASR'],
  // 只保留大模型 ASR。paraformer-v2 已移除：
  // 实测（同一段 433s 中文口播，14 个专名判据点）它的专名准确率只有 10.5%，
  // 而 qwen3 是 92.9%——差 7.6 倍。它是上一代传统 ASR 架构，没有世界知识，
  // 遇到"苏姿丰""World Labs"这类专名会崩。省钱不值得。
  models: ['qwen3-asr-flash-filetrans', 'fun-asr'],
  defaultModel: 'qwen3-asr-flash-filetrans',
  acceptsMp4: true,     // 实测：23MB mp4 直传成功
  acceptsUrl: true,     // 支持公网 URL，无需先下载
  needsFfmpeg: false,
  // 热词：阿里侧走 vocabulary_id，需要先在百炼控制台创建热词表拿 ID。
  // ⚠️ 实测：传一个不存在的 vocabulary_id 不会报错（任务照样 SUCCEEDED），
  //    也就是说**参数被静默忽略**——所以词表 ID 写错不会有任何提示，务必自己核对。
  hotwords: { mode: 'vocabulary_id', needsPreCreate: true },
  pricing: {
    unit: '秒',
    rate: 0.00008,
    note: 'qwen3-asr 系列按音频时长计费，约 ¥0.00008/秒量级。以官网为准。',
  },

  detect(env) {
    const key = this.envKeys.map((k) => env[k]).find((v) => v && v.length > 10);
    if (!key) {
      return {
        available: false,
        reason: '没有找到 DASHSCOPE_API_KEY',
        hint: '在 https://bailian.console.aliyun.com/ 开通百炼并创建 API Key，'
          + '然后写入 ~/.claude/api-vault.env 或导出为环境变量。',
        docs: 'https://help.aliyun.com/zh/model-studio/getting-started/',
      };
    }
    return { available: true, detail: `使用 ${this.envKeys.find((k) => env[k])}`, key };
  },

  /** 提交并等待结果。url 与 file 二选一（url 优先）。 */
  async transcribe({ key, url = null, file = null, model = null, language = 'zh',
    hotwords = [], corpusText = null, vocabularyId = null, onProgress = () => {} }) {
    const useModel = model || this.defaultModel;
    let fileUrl = url;

    if (!fileUrl) {
      if (!file) throw new Error('bailian: 需要提供 url 或 file');
      onProgress('上传音频到百炼临时存储…');
      fileUrl = await this.uploadInternal(key, file, useModel);
    }

    const single = SINGLE_OBJECT_MODELS.test(useModel);
    const parameters = {
      language_hints: language ? [language] : undefined,
      channel_id: [0],
    };
    // 热词：异步接口（filetrans）走 parameters.corpus.text。
    //
    // ⚠️ 这里踩过两个坑，都实测过：
    //   1. `input.messages`（同步版 qwen3-asr-flash 的写法）**在这里直接报错**：
    //      InvalidParameter.MalformedURL / "A valid file URL is required."
    //      —— 异步接口只认 input.file_url。
    //   2. 试过 `parameters.context`，任务会 SUCCEEDED 但**热词完全无效**（输出与无热词逐字相同）。
    //      这就是"未知参数被静默忽略"的陷阱。
    //
    // 实测有效的写法（苏姿丰从"8 对 2 错"变成"10 对 0 错"）：
    //   "parameters": { "corpus": { "text": "苏姿丰, 李飞飞, World Labs" } }
    //
    // corpus.text 接受任意文本（官方称"高度容错"），所以能像 Speak Low 那样写音近提示，
    // 例如「Qwen3（千问三／千万三）」引导模型输出正确拼写。
    if (corpusText) {
      parameters.corpus = { text: corpusText };
      onProgress(`携带热词上下文（${corpusText.length} 字符）`);
    } else if (hotwords?.length) {
      parameters.corpus = { text: hotwords.join(', ') };
      onProgress(`携带 ${hotwords.length} 个热词`);
    }
    // vocabulary_id 仍保留支持（预编译词表），但优先级低于 corpus
    if (vocabularyId && !parameters.corpus) {
      parameters.vocabulary_id = vocabularyId;
      onProgress(`使用预编译热词表 ${vocabularyId}`);
    }

    const body = {
      model: useModel,
      input: single ? { file_url: fileUrl } : { file_urls: [fileUrl] },
      parameters,
    };

    onProgress(`提交转写任务（${useModel}）…`);
    const submit = await this.postInternal(ENDPOINT, key, body, {
      needsOssResolve: String(fileUrl).startsWith('oss://'),
    });
    const taskId = submit?.output?.task_id;
    if (!taskId) throw new Error(`bailian: 提交失败 ${JSON.stringify(submit).slice(0, 300)}`);

    onProgress(`任务 ${taskId} 排队中…`);
    const out = await this.pollInternal(key, taskId, onProgress);

    const transcriptionUrl = single
      ? out?.result?.transcription_url
      : out?.results?.[0]?.transcription_url;
    if (!transcriptionUrl) {
      throw new Error(`bailian: 任务完成但拿不到结果地址 ${JSON.stringify(out).slice(0, 300)}`);
    }

    // 结果 JSON 24 小时后失效，必须立刻取回
    const r = await fetch(transcriptionUrl);
    if (!r.ok) throw new Error(`bailian: 下载结果失败 HTTP ${r.status}`);
    const data = await r.json();
    const transcripts = data.transcripts ?? [];
    const text = transcripts.map((t) => t.text).filter(Boolean).join('\n').trim();
    if (!text) throw new Error(`bailian: 结果里没有文本（可能整段静音）${JSON.stringify(data).slice(0, 200)}`);

    const sentences = transcripts.flatMap((t) => t.sentences ?? []);
    // 时长推导：阿里这个接口**不返回音频时长**（audio_info 里只有 format/sample_rate）。
    // 但分句带毫秒时间戳，取末句 end_time 即可——实测与视频真实时长吻合（433440ms vs 433.45s）。
    const lastEnd = sentences.length ? sentences[sentences.length - 1].end_time : 0;
    const durationSec = lastEnd ? lastEnd / 1000 : 0;

    return { text, model: useModel, sentences, durationSec, raw: data };
  },

  async postInternal(url, key, body, { needsOssResolve = false } = {}) {
    const headers = {
      Authorization: `Bearer ${key}`,
      'Content-Type': 'application/json',
      'X-DashScope-Async': 'enable',
    };
    // ⚠️ 当 file_url 是 oss:// 开头时（本地文件走临时上传后的形态），
    // 这个头是**必需**的。漏了会报 FILE_DOWNLOAD_FAILED，
    // 而报错信息完全不提是缺这个头——极难排查。见 alibaba-bailian-asr skill 的 file-upload-temp.md。
    if (needsOssResolve) headers['X-DashScope-OssResourceResolve'] = 'enable';
    const res = await fetch(url, {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(60000),
    });
    const t = await res.text();
    try { return JSON.parse(t); } catch { throw new Error(`bailian: 非 JSON 响应 ${t.slice(0, 200)}`); }
  },

  async pollInternal(key, taskId, onProgress) {
    const deadline = Date.now() + 30 * 60 * 1000;
    let last = '';
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 2000));
      const res = await fetch(`${TASK_URL}/${taskId}`, {
        headers: { Authorization: `Bearer ${key}` },
        signal: AbortSignal.timeout(30000),
      });
      const j = await res.json();
      const st = j?.output?.task_status;
      if (st !== last) { onProgress(`任务状态：${st}`); last = st; }
      if (st === 'SUCCEEDED') return j.output;
      if (st === 'FAILED' || st === 'UNKNOWN') {
        throw new Error(`bailian: 任务失败 ${JSON.stringify(j.output).slice(0, 300)}`);
      }
    }
    throw new Error('bailian: 轮询超时（30 分钟）');
  },

  /**
   * 上传本地文件到百炼临时存储，拿 oss:// 地址。
   * 用官方「临时上传」通道，不需要用户自备 OSS。
   */
  async uploadInternal(key, filePath, model) {
    const { readFileSync } = await import('node:fs');
    const { basename } = await import('node:path');

    const policyRes = await fetch(`${UPLOAD_POLICY}?action=getPolicy&model=${encodeURIComponent(model)}`, {
      headers: { Authorization: `Bearer ${key}` },
      signal: AbortSignal.timeout(30000),
    });
    const policy = await policyRes.json();
    const d = policy?.data;
    if (!d?.upload_host) throw new Error(`bailian: 取上传凭证失败 ${JSON.stringify(policy).slice(0, 200)}`);

    // 文件名必须净化：含中文/空格时服务端会报 InvalidFile.DownloadFailed
    const safeName = basename(filePath).replace(/[^\w.-]/g, '_');
    const objectKey = `${d.upload_dir}/${safeName}`;

    const buf = readFileSync(filePath);
    const form = new FormData();
    form.append('OSSAccessKeyId', d.oss_access_key_id);
    form.append('Signature', d.signature);
    form.append('policy', d.policy);
    form.append('x-oss-object-acl', d.x_oss_object_acl);
    form.append('x-oss-forbid-overwrite', d.x_oss_forbid_overwrite);
    form.append('key', objectKey);
    form.append('success_action_status', '200');
    form.append('file', new Blob([buf]), safeName);

    const up = await fetch(d.upload_host, { method: 'POST', body: form, signal: AbortSignal.timeout(600000) });
    if (!up.ok) throw new Error(`bailian: 上传失败 HTTP ${up.status} ${(await up.text()).slice(0, 200)}`);
    return `oss://${objectKey}`;
  },
};
