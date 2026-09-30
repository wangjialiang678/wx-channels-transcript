/**
 * 热词（自定义词表）的提取与传递。
 *
 * 为什么需要它：ASR 最容易错的是**英文专名和中文同音字**——
 * 实测同一段音频，两个模型都把「苏姿丰」写成「朱志峰」。
 * 而喂进热词后，火山的输出把「苏姿丰」全部纠正了。
 *
 * 这个模块的核心思路：**视频号标题里往往就写着关键专名**。
 * 例如：
 *   82亿美元，苏姿丰买下李飞飞的 WolrdLabs。#李飞飞 #苏姿丰 #AMD #人工智能 #AI #WorldLabs #科技前沿
 * 从中能自动提取出 李飞飞 / 苏姿丰 / AMD / WorldLabs —— 正好是 ASR 容易错的那批词。
 */

/** 从标题里提取候选热词。 */
export function extractHotwords(title = '', { extra = [], max = 50 } = {}) {
  const words = new Set();

  // 1) 话题标签：#李飞飞 #AMD #WorldLabs
  for (const m of title.matchAll(/#([^\s#，,。.、；;！!？?|]+)/g)) {
    const w = m[1].trim();
    if (w) words.add(w);
  }

  // 2) 连续大写英文字母 / 数字混排（≥2 字符）：AMD、ChatGPT、ImageNet、WorldLabs、CES、MIT
  for (const m of title.matchAll(/\b([A-Z][A-Za-z0-9]{1,})\b/g)) {
    words.add(m[1]);
  }

  // 3) 用户额外指定
  for (const w of extra) if (w) words.add(String(w).trim());

  // 4) 过滤：太短的、纯通用的
  const STOP = new Set(['AI', 'A', 'I', 'THE', 'AND', 'OF', 'TO', 'IN', 'IS',
    '人工智能', '科技', '前沿', '科技前沿', '视频', '推荐', '热门', '分享']);
  const out = [...words]
    .filter((w) => w.length >= 2 && !STOP.has(w.toUpperCase()) && !STOP.has(w))
    .slice(0, max);

  return out;
}

/** 解析 --hotwords "词1,词2" 或 "词1 词2" 或文件路径。 */
export async function parseHotwordsArg(arg) {
  if (!arg || arg === true) return [];
  const s = String(arg).trim();
  const { existsSync, readFileSync } = await import('node:fs');
  if (existsSync(s)) {
    // 支持每行一个词 或 逗号分隔
    return readFileSync(s, 'utf8')
      .split(/[\n,]/).map((x) => x.trim()).filter(Boolean);
  }
  return s.split(/[,，\s]+/).map((x) => x.trim()).filter(Boolean);
}

/**
 * 把热词按各后端的能力形式化。
 *
 * 三家支持的形态完全不同：
 *   · 火山   → corpus.context 直传 JSON 字符串，最多 5000 词（最方便，实测有效）
 *   · 阿里   → vocabulary_id，需先在控制台建词表拿 ID（多一步，且 ID 写错会被静默忽略）
 *   · 本地   → whisper 无原生热词（可用 --prompt 初始提示词近似实现）
 */
export function toBackendPayload(backendId, hotwords, opts = {}) {
  if (!hotwords?.length) return null;

  switch (backendId) {
    case 'volcengine': {
      // 官方格式：corpus.context = "{\"hotwords\":[{\"word\":\"...\"}]}"
      const ctx = JSON.stringify({ hotwords: hotwords.map((w) => ({ word: w })) });
      return { corpus: { context: ctx } };
    }
    case 'bailian': {
      // 需要预先创建的词表 ID；用户没提供就没法用
      return opts.vocabularyId ? { vocabulary_id: opts.vocabularyId } : null;
    }
    case 'local': {
      // whisper.cpp 没有热词，但可用初始提示词（--prompt）引导
      return { prompt: `以下是普通话内容，可能涉及这些专有名词：${hotwords.join('、')}。` };
    }
    default:
      return null;
  }
}

/** 人类可读的能力说明，用于 CLI 提示。 */
export const HOTWORD_CAPABILITY = {
  volcengine: { support: 'direct', limit: 5000, note: '热词直传（corpus.context），无需预建词表' },
  bailian: { support: 'corpus', limit: 10000, note: '走 parameters.corpus.text，接受任意文本，无需预建词表（实测有效）' },
  local: { support: 'prompt', limit: null, note: 'whisper 无原生热词，用初始提示词近似' },
};

/**
 * 构造送给阿里的 corpus.text。
 *
 * 阿里对这段文本是**高度容错**的（官方原话："甚至无意义的文本也不会影响识别"），
 * 所以可以写得比纯词表更"有信息量"——这借鉴了 Speak Low 的实践：
 * 在热词后面附加**中文音近说法**，引导模型输出正确拼写。
 *
 *   苏姿丰、李飞飞、World Labs（沃尔德的实验室）、Qwen3（千问三／千万三）
 *
 * 条目支持两种写法：
 *   "苏姿丰"                      纯热词
 *   "Qwen3(千问三/千万三)"         带音近提示（括号内为可能听到的读法）
 */
export function buildCorpusText(hotwords, { intro = null } = {}) {
  if (!hotwords?.length) return null;
  const head = intro
    ?? '本次录音涉及以下专有名词。如果听到与括号内读音相近的内容，请输出括号外的正确写法：';
  return `${head}\n${hotwords.join('、')}`;
}

/** 从统一的后端能力表里取某后端的热词能力（给 CLI 做提示用）。 */
export function capabilityOf(backendId) {
  return HOTWORD_CAPABILITY[backendId] ?? null;
}
