/**
 * ASR 后端的注册与选择。
 *
 * 选择原则（按「用户需要多做几步」排序，越少越优先）：
 *   1. 云端 + 公网URL直传 + 能直接吃 mp4   → 零额外步骤（阿里云百炼、火山小模型）
 *   2. 云端 + 公网URL直传 + 只收音频格式   → 需要 ffmpeg 抽音频（火山大模型）
 *   3. 本地模型                            → 需要下载 + ffmpeg + 本地算力
 *
 * 一句话：**优先云端**（快、准、便宜），本地作为没有凭证时的兜底。
 */

import bailian from './bailian.mjs';
import local from './local.mjs';
import volcengine, { smallmodel as volcengineSmall } from './volcengine.mjs';

export const BACKENDS = {
  bailian,
  volcengine,
  'volcengine-small': volcengineSmall,   // 保留实现，但见下方说明
  local,
};

/**
 * 参与「自动探测 + 推荐」的后端顺序。
 *
 * 只收录**第二代（大模型 ASR）**：
 *   bailian        qwen3-asr —— 默认，最快
 *   volcengine     volc.seedasr.auc 2.0 —— 同级，热词直传更方便
 *   local          whisper —— 数据不出本机的兜底
 *
 * 刻意排除的两条（都是**第一代传统 ASR**，没有世界知识，专名会崩）：
 *   · paraformer-v2（阿里）    —— 已从 bailian.mjs 的 models 列表删除
 *   · volcengine-small（火山） —— 老体系，实测未验证；实现仍保留在 volcengine.mjs，
 *                                 需要时可显式 `-t volcengine-small` 调用
 *
 * 判断依据是一次同口径实测（433s 中文口播，14 个专名判据点）：
 *   qwen3-asr 92.9% ｜ volc.seedasr 93.0% ｜ paraformer-v2 10.5%
 */
export const ORDER = ['bailian', 'volcengine', 'local'];

export function getBackend(id) {
  const b = BACKENDS[id];
  if (!b) {
    throw new Error(`未知的 ASR 后端：${id}。可选：${Object.keys(BACKENDS).join(', ')}`);
  }
  return b;
}

/** 这个后端要跑通，总共需要用户额外做几步。 */
export function extraSteps(backend) {
  const steps = [];
  if (!backend.acceptsUrl) steps.push('先把媒体下载到本地');
  if (!backend.acceptsMp4) steps.push('用 ffmpeg 抽取音频');
  return steps;
}

/**
 * 探测所有后端在本机是否可用。
 * @returns {Array<{id, label, available, detail?, reason?, hint?, docs?, extraSteps, backend}>}
 */
export function detectAll(env) {
  return ORDER.map((id) => {
    const b = BACKENDS[id];
    let r;
    try {
      r = b.detect(env);
    } catch (e) {
      r = { available: false, reason: `探测出错：${e.message}` };
    }
    return { id, label: b.label, ...r, extraSteps: extraSteps(b), backend: b };
  });
}

/**
 * 给出推荐：返回排序后的可用后端，并附上「为什么推荐它」的人话解释。
 * @param {Array} detected  detectAll() 的结果
 * @param {object} opts     { hasFfmpeg: boolean }
 */
export function recommend(detected, { hasFfmpeg = false } = {}) {
  const usable = detected.filter((d) => d.available);
  if (usable.length === 0) {
    return { chosen: null, ranked: [], reason: '本机没有检测到任何可用的 ASR 后端。' };
  }

  const scored = usable.map((d) => {
    let score = 0;
    const notes = [];
    if (d.index === 0 || d.id === 'bailian') score += 100; // 云端优先
    if (d.id !== 'local') score += 50;
    if (d.backend.acceptsUrl) { score += 30; notes.push('支持公网直链直传，不用先下载'); }
    if (d.backend.acceptsMp4) { score += 25; notes.push('能直接吃 mp4，不需要 ffmpeg'); }
    if (d.backend.needsFfmpeg && !hasFfmpeg) { score -= 40; notes.push('⚠️ 本机缺 ffmpeg，这条路会卡住'); }
    if (d.id === 'local') { score -= 60; notes.push('本地推理，慢且吃算力'); }
    return { ...d, score, notes };
  }).sort((a, b) => b.score - a.score);

  const top = scored[0];
  const why = [];
  why.push(`${top.label} 已就绪（${top.detail ?? ''}）。`);
  if (top.backend.acceptsUrl && top.backend.acceptsMp4) {
    why.push('它既能接受公网直链、又能直接处理 mp4，所以整条链路可以做到'
      + '「不下载、不转码」，是最快的一条。');
  } else if (top.backend.acceptsUrl) {
    why.push('它能接受公网直链，但只收音频格式，所以需要先用 ffmpeg 抽一次音频。');
  } else {
    why.push('它只能处理本地文件，所以需要先把媒体下载下来。');
  }
  if (scored.length > 1) {
    const alts = scored.slice(1).map((s) => s.label).join('、');
    why.push(`备选：${alts}。`);
  }

  return { chosen: top, ranked: scored, reason: why.join('') };
}
