/**
 * 结果落盘。
 *
 * 输出两份：
 *   · `YYYY-MM-DD-<标题>.md`  —— 人读的逐字稿（带来源、时长等元信息）
 *   · `<同名>.json`           —— 机器读的（含分句时间戳，便于做字幕或二次加工）
 */

import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

/** 把标题变成安全的文件名片段。 */
export function slugify(title, fallback, maxlen = 32) {
  const t = String(title || '')
    .replace(/[\s#？?！!，,。.、：:；;“”"'（）()\[\]【】/\\|<>*]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
  return t.slice(0, maxlen) || fallback;
}

function fmtDuration(sec) {
  if (!sec || !Number.isFinite(sec)) return '未知';
  const s = Math.round(sec);
  return s < 60 ? `${s} 秒` : `${Math.floor(s / 60)} 分 ${s % 60} 秒`;
}

export function writeOutputs({ outDir, info, transcript, backend, timing }) {
  mkdirSync(outDir, { recursive: true });
  const stamp = new Date().toISOString().slice(0, 10);
  const base = `${stamp}-${slugify(info.title, info.shareId || 'transcript')}`;
  const mdPath = join(outDir, `${base}.md`);
  const jsonPath = join(outDir, `${base}.json`);

  const lines = [
    '---',
    `title: ${info.title || '(无标题)'}`,
    `source_url: ${info.url}`,
    `share_id: ${info.shareId}`,
    info.author ? `author: ${info.author}` : null,
    `duration: ${fmtDuration(info.duration)}`,
    `extracted_at: ${new Date().toISOString()}`,
    `asr_backend: ${backend.id}`,
    `asr_model: ${transcript.model}`,
    `elapsed_seconds: ${timing.total.toFixed(1)}`,
    'asr_note: ASR 原始输出，未人工校对，同音字可能有误',
    '---',
    '',
    `# ${info.title || '逐字稿'}`,
    '',
    `> 来源：${info.url}`,
    info.author ? `> 作者：${info.author}` : null,
    `> 时长：${fmtDuration(info.duration)}　|　转写：${transcript.model}　|　耗时：${timing.total.toFixed(1)} 秒`,
    '>',
    '> 文字为语音识别原始输出，**未做人工校对**。要精确引用请回原视频核对。',
    '',
    '---',
    '',
    transcript.text,
    '',
  ].filter((x) => x !== null);

  writeFileSync(mdPath, lines.join('\n'), 'utf8');
  writeFileSync(jsonPath, JSON.stringify({
    source: info,
    backend: { id: backend.id, label: backend.label, model: transcript.model },
    timing,
    text: transcript.text,
    sentences: transcript.sentences ?? [],
  }, null, 2), 'utf8');

  return { mdPath, jsonPath, textLength: transcript.text.length };
}
