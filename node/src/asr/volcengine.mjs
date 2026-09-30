/**
 * ASR 后端：火山引擎（字节跳动 / 豆包语音）—— 两条路线。
 *
 * ⚠️ 首先要分清两条**互不相通**的产品线（这是最容易踩的坑）：
 *   · 「豆包语音」控制台 → 真 ASR。鉴权用 X-Api-Key（新版）或 APPID+Token（旧版）。
 *   · 「火山方舟」平台   → 多模态 LLM 的"音频理解"。用 ark- 开头的 Key。
 *   两者不通用：**ark- Key 调不了豆包语音的 ASR**。本文件只处理前者。
 *
 * 本文件实现两个后端：
 *
 *   volcengine（大模型，推荐）
 *     · 提交 /api/v3/auc/bigmodel/submit，查询 /query
 *     · volc.seedasr.auc = 模型2.0（中文最好），volc.bigasr.auc = 1.0
 *     · ⚠️ 音频格式白名单 wav/mp3/ogg/spx/amr/aac/m4a —— **不含 mp4**
 *     · 支持 audio.url 直接提交公网链接
 *
 *   volcengine-small（小模型，唯一官方写明支持 mp4 的录音识别接口）
 *     · 提交 /api/v1/auc/submit，查询 /query
 *     · 格式：wav / ogg / mp3 / **mp4**（官方原文）
 *     · 代价：识别质量是 1.0 之前的旧模型，鉴权走 APPID+Token+cluster 老体系
 *     · 优势：**可以完全不碰 ffmpeg**
 *
 * 一手来源：
 *   https://www.volcengine.com/docs/DoubaoVoice/Accessmust-read                （三档模型对比 + 格式白名单）
 *   https://www.volcengine.com/docs/DoubaoVoice/LargemodelrecordingfilerecognitionstandardversionAPI
 *   https://www.volcengine.com/docs/DoubaoVoice/AudioFileRecognitionStandardEdition  （小模型，写明支持 mp4）
 */

import { randomUUID } from 'node:crypto';

const SUBMIT_V3 = 'https://openspeech.bytedance.com/api/v3/auc/bigmodel/submit';
const QUERY_V3 = 'https://openspeech.bytedance.com/api/v3/auc/bigmodel/query';
const SUBMIT_V1 = 'https://openspeech.bytedance.com/api/v1/auc/submit';
const QUERY_V1 = 'https://openspeech.bytedance.com/api/v1/auc/query';

/** 大模型线的资源 ID */
const RESOURCE = { '2.0': 'volc.seedasr.auc', '1.0': 'volc.bigasr.auc' };

const COMMON_ENV_KEYS = ['VOLC_ASR_API_KEY', 'VOLC_SPEECH_API_KEY', 'VOLC_TTS_API_KEY'];
const LEGACY_ENV = ['VOLC_ASR_APP_ID', 'VOLC_ASR_ACCESS_TOKEN', 'VOLC_ASR_CLUSTER'];

const SETUP_HINT = `在「豆包语音」控制台开通并取 Key（注意：不是火山方舟的 ark- Key）：
  1. 注册/登录火山引擎  https://console.volcengine.com/auth/signup
  2. 完成实名认证        https://console.volcengine.com/user/realname
  3. 进入新版豆包语音控制台并开通「录音文件识别大模型 - 标准版」
                        https://console.volcengine.com/speech/new
                        （有 20 小时/半年 免费额度，控制台点「试用」领取）
  4. 创建 API Key        https://console.volcengine.com/speech/new/setting/apikeys
  5. 写入环境变量        echo 'VOLC_ASR_API_KEY=你的key' >> ~/.claude/api-vault.env`;

// ─────────────────────────────────────────────── 大模型线（推荐）
export const bigmodel = {
  id: 'volcengine',
  label: '火山引擎·豆包语音（大模型标准版）',
  envKeys: COMMON_ENV_KEYS,
  models: ['2.0', '1.0'],
  defaultModel: '2.0',
  // ⚠️ 官方格式白名单（Accessmust-read）写的是 wav/mp3/ogg/spx/amr/aac/m4a，**不含 mp4**。
  // 但 2026-09-30 实测：直接提交 mp4 直链 + format:"mp4" **成功**，
  // audio_info.duration 正确返回 433426ms（与视频时长吻合）。
  // 服务端显然做了容器兼容（mp4 与 m4a 同属 ISO BMFF，读的是内部音轨）。
  // 以实测为准：这里标 true，因此本地不需要 ffmpeg。
  acceptsMp4: true,
  acceptsUrl: true,
  needsFfmpeg: false,
  pricing: { unit: '小时', rate: 2.3, note: '标准版 2.3 元/小时；资源包可压到 0.7~0.8；20 小时/半年免费额度。' },

  detect(env) {
    const key = COMMON_ENV_KEYS.map((k) => env[k]).find((v) => v && v.length > 10);
    if (key) return { available: true, detail: `使用 ${COMMON_ENV_KEYS.find((k) => env[k])}`, key, mode: 'new' };
    const [appId, token, cluster] = LEGACY_ENV.map((k) => env[k]);
    if (appId && token) {
      return { available: true, detail: '使用 APP ID + Access Token（旧版控制台）', mode: 'legacy', appId, token, cluster };
    }
    return {
      available: false,
      reason: '没有找到豆包语音的凭证',
      hint: SETUP_HINT,
      docs: 'https://www.volcengine.com/docs/6561/1354868',
      note: '⚠️ 火山方舟的 ARK_API_KEY 不能用于此服务，两者是不同产品线。',
    };
  },

  async transcribe({ key, mode = 'new', appId, token, url = null, file = null,
    model = null, language = 'zh-CN', hotwords = [], onProgress = () => {} }) {
    if (!url) {
      throw new Error('volcengine: 这条后端只接受公网 URL（audio.url）。\n'
        + '  本地文件请改用 -t bailian（它支持上传本地文件），或先把文件放到可公开访问的位置。');
    }
    const resourceId = RESOURCE[model?.includes('1.0') ? '1.0' : '2.0'];
    const requestId = randomUUID();
    const headers = this.authHeaders({ key, mode, appId, token, resourceId, requestId });

    // 直链通常是 mp4（视频号给的就是这个）。实测服务端能吃 mp4，
    // 所以不必先抽音；但保留按扩展名推断，方便将来接其它源。
    const fmt = /\.(wav|mp3|ogg|m4a|spx|amr|aac)(\?|$)/i.exec(url)?.[1]?.toLowerCase() ?? 'mp4';

    // 热词：corpus.context 直传（官方示例 "{\"hotwords\":[{\"word\":\"...\"}]}"）
    // 实测有效：喂入「苏姿丰」后，原本错成"朱志峰"的地方全部纠正。
    const corpus = {};
    if (hotwords?.length) {
      corpus.context = JSON.stringify({ hotwords: hotwords.map((w) => ({ word: w })) });
      onProgress(`携带 ${hotwords.length} 个热词：${hotwords.slice(0, 8).join('、')}${hotwords.length > 8 ? '…' : ''}`);
    }

    const body = {
      user: { uid: 'sph-transcript' },
      audio: { format: fmt, url },
      request: {
        model_name: 'bigmodel', enable_itn: true, enable_punc: true,
        ...(language ? { language } : {}),
        ...(Object.keys(corpus).length ? { corpus } : {}),
      },
    };

    onProgress('提交火山转写任务…');
    const sub = await fetch(SUBMIT_V3, {
      method: 'POST', headers, body: JSON.stringify(body), signal: AbortSignal.timeout(60000),
    });
    const code = sub.headers.get('X-Api-Status-Code');
    if (code !== '20000000') {
      throw new Error(`volcengine: 提交失败 code=${code} msg=${sub.headers.get('X-Api-Message')}\n`
        + '  常见原因：未开通该服务 / Key 不对（注意 ark- Key 不适用）/ 未实名。');
    }

    onProgress(`任务 ${requestId} 排队中…`);
    const out = await this.pollV3({ key, mode, appId, token, resourceId, requestId, onProgress });
    const text = out?.result?.text;
    if (!text) throw new Error('volcengine: 返回里没有文本（可能整段静音）');
    // 火山直接返回音频时长（毫秒），比阿里侧只能从分句时间戳推更省事
    const ms = out?.audio_info?.duration;
    return {
      text,
      model: `volcengine-${resourceId.split('.').pop()}`,
      sentences: out?.result?.utterances ?? [],
      durationSec: ms ? ms / 1000 : 0,
      raw: out,
    };
  },

  authHeaders({ key, mode, appId, token, resourceId, requestId }) {
    const h = {
      'Content-Type': 'application/json',
      'X-Api-Resource-Id': resourceId,
      'X-Api-Request-Id': requestId,
      'X-Api-Sequence': '-1',
    };
    if (mode === 'legacy') {
      h['X-Api-App-Key'] = appId;
      h['X-Api-Access-Key'] = token;
    } else {
      h['X-Api-Key'] = key;
    }
    return h;
  },

  async pollV3({ key, mode, appId, token, resourceId, requestId, onProgress }) {
    const deadline = Date.now() + 30 * 60 * 1000;
    let last = '';
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 2000));
      const h = this.authHeaders({ key, mode, appId, token, resourceId, requestId });
      delete h['X-Api-Sequence'];
      const q = await fetch(QUERY_V3, { method: 'POST', headers: h, body: '{}', signal: AbortSignal.timeout(30000) });
      const code = q.headers.get('X-Api-Status-Code');
      if (code !== last) { onProgress(`任务状态：${code}`); last = code; }
      if (code === '20000000') return await q.json();
      if (code === '20000001' || code === '20000002') continue;
      if (code === '20000003') throw new Error('volcengine: 音频是静音，未检测到人声');
      if (code && !code.startsWith('20000')) {
        throw new Error(`volcengine: 任务失败 code=${code} msg=${q.headers.get('X-Api-Message')}`);
      }
    }
    throw new Error('volcengine: 轮询超时（30 分钟）');
  },
};

// ─────────────────────────────────────────────── 小模型线（唯一能吃 mp4）
export const smallmodel = {
  id: 'volcengine-small',
  label: '火山引擎·录音文件识别（小模型，支持 mp4）',
  envKeys: LEGACY_ENV,
  models: ['standard'],
  defaultModel: 'standard',
  acceptsMp4: true,    // ✅ 官方原文：wav / ogg / mp3 / mp4
  acceptsUrl: true,
  needsFfmpeg: false,  // 这是它最大的价值
  pricing: { unit: '小时', rate: 1.8, note: '标准版 1.8 元/小时起；20 小时/半年免费额度。' },

  detect(env) {
    const [appId, token, cluster] = LEGACY_ENV.map((k) => env[k]);
    // 官方文档里 app.appid / app.token / app.cluster 三个字段全部标了「必填」，
    // 少任何一个服务端都会拒，所以这里三个都要有才算可用。
    if (appId && token && cluster) {
      return {
        available: true,
        detail: `APP ID + Token + Cluster（老体系鉴权）`,
        mode: 'legacy', appId, token, cluster,
      };
    }
    const missing = [];
    if (!appId) missing.push('VOLC_ASR_APP_ID');
    if (!token) missing.push('VOLC_ASR_ACCESS_TOKEN');
    if (!cluster) missing.push('VOLC_ASR_CLUSTER');
    return {
      available: false,
      reason: `小模型线需要三个字段（老体系），缺：${missing.join('、')}`,
      hint: `小模型的三个凭证**不在**新版 API Key 页面，而在控制台的「创建应用 / 开通服务」入口：
  1. 进入豆包语音控制台  https://console.volcengine.com/speech/new
  2. 创建应用并开通「录音文件识别（标准版）」
  3. 在应用详情里复制 APP ID、Access Token、Cluster ID 三个字段
  4. 写入环境变量：
       echo 'VOLC_ASR_APP_ID=...'       >> ~/.claude/api-vault.env
       echo 'VOLC_ASR_ACCESS_TOKEN=...' >> ~/.claude/api-vault.env
       echo 'VOLC_ASR_CLUSTER=...'      >> ~/.claude/api-vault.env

  ⚠️ 注意：新版控制台的 VOLC_ASR_API_KEY（X-Api-Key）**只能调大模型接口**，
     不能用于小模型；两者是两套互不通用的鉴权体系。`,
      docs: 'https://www.volcengine.com/docs/DoubaoVoice/AudioFileRecognitionStandardEdition',
      note: '小模型的官方请求体要求 app.appid / app.token / app.cluster 三个字段全部必填。',
    };
  },

  async transcribe({ appId, token, cluster, url = null, file = null,
    language = 'zh-CN', onProgress = () => {} }) {
    if (!url) throw new Error('volcengine-small: 需要公网 URL（audio.url）。');

    const body = {
      app: { appid: appId, token, cluster: cluster || 'volcengine_input_common' },
      user: { uid: 'sph-transcript' },
      audio: { format: 'mp4', url },
      additions: {
        use_itn: 'True',
        with_speaker_info: 'False',
      },
      request: { model_name: 'bigmodel', enable_itn: true, enable_punc: true },
    };

    onProgress('提交火山小模型转写任务…');
    const sub = await fetch(SUBMIT_V1, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer; ${token}` },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(60000),
    });
    const sj = await sub.json().catch(() => null);
    const taskId = sj?.resp?.id ?? sj?.id;
    const subCode = sj?.code ?? sj?.resp?.code;
    if (!taskId || (subCode !== undefined && subCode !== 1000)) {
      throw new Error(`volcengine-small: 提交失败 ${JSON.stringify(sj).slice(0, 300)}\n`
        + '  常见原因：APP ID / Token / cluster 任一不对，或该模型未开通。');
    }

    onProgress(`任务 ${taskId} 处理中…`);
    const deadline = Date.now() + 30 * 60 * 1000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 3000));
      const q = await fetch(QUERY_V1, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer; ${token}` },
        body: JSON.stringify({ appid: appId, token, cluster: cluster || 'volcengine_input_common', id: taskId }),
        signal: AbortSignal.timeout(30000),
      });
      const qj = await q.json().catch(() => null);
      const code = qj?.code ?? qj?.resp?.code;
      if (code === 1000) return this.parseResult(qj, onProgress);
      if (code === 1001 || code === 1002) continue; // 处理中
      if (code === 1003) throw new Error('volcengine-small: 访问超频（超出并发配额），稍后重试');
      if (code && code !== 1000) {
        throw new Error(`volcengine-small: 任务失败 code=${code} msg=${qj?.message ?? ''}`);
      }
    }
    throw new Error('volcengine-small: 轮询超时（30 分钟）');
  },

  /** 老接口的结果没有公开稳定 schema，做宽松解析。 */
  parseResult(qj, onProgress) {
    const utterances = qj?.resp?.utterances ?? qj?.utterances ?? [];
    const text = (qj?.resp?.text || qj?.text)
      || utterances.map((u) => u.text).join('');
    if (!text) {
      onProgress('提示：结果结构可能已变化，原始返回已写入 raw 字段');
      throw new Error(`volcengine-small: 返回里没有文本 ${JSON.stringify(qj).slice(0, 300)}`);
    }
    return { text, model: 'volcengine-small', sentences: utterances, raw: qj };
  },
};

export default bigmodel;
