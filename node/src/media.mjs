/**
 * 媒体处理：下载、抽音频。
 *
 * 设计原则：**每一步都按需触发，能省就省**。
 *   · 选中的 ASR 支持公网直链 + 能吃 mp4（如阿里云）→ 本模块完全不被调用
 *   · 后端只收音频 → 下载 + 抽音频
 *   · 用户显式要音频/视频（--save-audio / --save-video）→ 即使后端不需要，也照做
 *
 * 中间文件的去留由调用方决定（转写完成后再清理），因为音频在转写结束前不能删。
 */

import { spawnSync } from 'node:child_process';
import { copyFileSync, createWriteStream, existsSync, mkdirSync, rmSync, statSync, unlinkSync } from 'node:fs';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { join } from 'node:path';

/** 支持的音频输出格式 → ffmpeg 编码参数与扩展名。 */
export const AUDIO_FORMATS = {
  wav: { ext: 'wav', args: ['-c:a', 'pcm_s16le'] },
  mp3: { ext: 'mp3', args: ['-c:a', 'libmp3lame', '-q:a', '4'] },
  m4a: { ext: 'm4a', args: ['-c:a', 'aac', '-b:a', '128k'] },
};

export function hasFfmpeg() {
  return spawnSync('which', ['ffmpeg'], { encoding: 'utf8' }).status === 0;
}

function ffmpegBin() {
  const r = spawnSync('which', ['ffmpeg'], { encoding: 'utf8' });
  if (r.status !== 0) {
    throw new Error('需要 ffmpeg 但本机没有安装。\n'
      + '  安装：brew install ffmpeg\n'
      + '  或者改用「能直链直传且能吃 mp4」的云端后端（如阿里云百炼），可以完全跳过这一步。');
  }
  return r.stdout.trim();
}

/** 下载媒体到本地。直链自带签名，不需要任何 cookie。 */
export async function download(mediaUrl, destPath, { onProgress = () => {} } = {}) {
  mkdirSync(join(destPath, '..'), { recursive: true });
  onProgress('下载媒体…');
  const res = await fetch(mediaUrl, { signal: AbortSignal.timeout(30 * 60 * 1000) });
  if (!res.ok) throw new Error(`下载失败 HTTP ${res.status}`);
  await pipeline(Readable.fromWeb(res.body), createWriteStream(destPath));
  const size = statSync(destPath).size;
  onProgress(`已下载 ${(size / 1048576).toFixed(1)} MB`);
  return { path: destPath, size };
}

/**
 * 抽取音频。默认转 16kHz 单声道（ASR 通用规格）；
 * 若指定 format 为 mp3/m4a，则按该格式编码以便用户直接使用。
 */
export function extractAudio(srcPath, destPath, { format = 'wav', forAsr = true, onProgress = () => {} } = {}) {
  const spec = AUDIO_FORMATS[format] ?? AUDIO_FORMATS.wav;
  onProgress(`抽取音频（${format}${forAsr ? '，16kHz 单声道' : ''}）…`);
  const args = ['-y', '-v', 'error', '-i', srcPath, '-vn'];
  if (forAsr) args.push('-ac', '1', '-ar', '16000'); // ASR 要求：单声道 16k
  args.push(...spec.args, destPath);
  const r = spawnSync(ffmpegBin(), args, { encoding: 'utf8', timeout: 30 * 60 * 1000 });
  if (r.status !== 0 || !existsSync(destPath)) {
    throw new Error(`抽音频失败：${(r.stderr || '').slice(0, 300)}`);
  }
  return { path: destPath, size: statSync(destPath).size };
}

/**
 * 准备 ASR 输入。
 *
 * @param {string} mediaUrl   直链
 * @param {object} opts
 *   workDir       工作目录
 *   forAsr        音频是否为 ASR 规格（16k 单声道 wav）；默认 true
 *   audioFormat   当用户要保存音频时用的格式（wav/mp3/m4a）
 *   needDownload  是否必须拿到本地媒体文件（后端不支持直链时为 true）
 *   onProgress
 */
export async function prepareMedia(mediaUrl, {
  workDir,
  needDownload = true,
  needAudio = true,
  audioFormat = 'wav',
  onProgress = () => {},
} = {}) {
  mkdirSync(workDir, { recursive: true });
  const stamp = Date.now();
  const mediaPath = join(workDir, `source-${stamp}.mp4`);

  const result = { mediaPath: null, audioPath: null };

  if (needDownload || needAudio) {
    await download(mediaUrl, mediaPath, { onProgress });
    result.mediaPath = mediaPath;
  }

  if (needAudio) {
    const spec = AUDIO_FORMATS[audioFormat] ?? AUDIO_FORMATS.wav;
    const audioPath = join(workDir, `audio-${stamp}.${spec.ext}`);
    // ASR 需要 16k 单声道；若非 wav 且仅用于 ASR，也统一成 wav 更稳
    const asrSpec = audioFormat === 'wav';
    extractAudio(mediaPath, audioPath, { format: audioFormat, forAsr: asrSpec, onProgress });
    result.audioPath = audioPath;
  }

  return result;
}

/** 把文件搬到输出目录（保留时用），保留原扩展名。 */
export function promote(srcPath, outDir, baseName) {
  if (!srcPath || !existsSync(srcPath)) return null;
  mkdirSync(outDir, { recursive: true });
  const ext = srcPath.slice(srcPath.lastIndexOf('.'));
  const dest = join(outDir, `${baseName}${ext}`);
  copyFileSync(srcPath, dest);
  return dest;
}

/** 清理：只删调用方明确点名、且确实存在的路径。 */
export function cleanup(paths = []) {
  for (const p of paths) {
    if (p && existsSync(p)) {
      try { unlinkSync(p); } catch { /* 忽略 */ }
    }
  }
}

/** 删除整个临时工作目录。 */
export function cleanupDir(dir) {
  if (dir && existsSync(dir)) {
    try { rmSync(dir, { recursive: true, force: true }); } catch { /* 忽略 */ }
  }
}
