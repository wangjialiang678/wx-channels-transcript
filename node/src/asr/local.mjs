/**
 * ASR 后端：本地模型（whisper.cpp / sherpa-onnx）。
 *
 * 定位：**没有云端凭证时的兜底**，以及处理敏感素材时想完全离线。
 * 代价要说清楚：需要自己装推理引擎 + 下模型，且本地推理比云端慢得多。
 *
 * 走「外部可执行文件」路线而不是进程内推理，是为了保持本项目零 npm 依赖。
 */

import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

/** 已知的本地推理引擎可执行文件名（按优先级）。 */
const ENGINES = {
  whisper: ['whisper-cli', 'whisper-cpp', 'whisper'],
  sherpa: ['sherpa-onnx-offline', 'sherpa-onnx'],
};

const MODEL_HINTS = [
  join(homedir(), '.cache/whisper/ggml-small.bin'),
  join(homedir(), '.cache/whisper/ggml-base.bin'),
  join(homedir(), '.cache/whisper/ggml-medium.bin'),
  join(homedir(), 'models/ggml-small.bin'),
];

function which(cmd) {
  const r = spawnSync('which', [cmd], { encoding: 'utf8' });
  return r.status === 0 ? r.stdout.trim() : null;
}

function findWhisperCli() {
  for (const c of ENGINES.whisper) {
    const p = which(c);
    if (p) return p;
  }
  return null;
}

function findModel() {
  const envModel = process.env.WHISPER_MODEL;
  if (envModel && existsSync(envModel)) return envModel;
  for (const m of MODEL_HINTS) if (existsSync(m)) return m;
  // 兜底：扫 ~/.cache/whisper 下的 ggml-*.bin / *.pt
  const dir = join(homedir(), '.cache/whisper');
  if (existsSync(dir)) {
    const hit = readdirSync(dir).find((f) => /^ggml-.*\.bin$/i.test(f));
    if (hit) return join(dir, hit);
  }
  return null;
}

export default {
  id: 'local',
  label: '本地模型（whisper.cpp）',
  envKeys: [],
  models: ['whisper-small', 'whisper-medium', 'sensevoice'],
  defaultModel: 'whisper-small',
  acceptsMp4: false,   // 必须先抽成 16kHz wav
  acceptsUrl: false,   // 只能吃本地文件
  needsFfmpeg: true,   // 抽音频 + 转 16k 单声道
  pricing: { unit: '次', rate: 0, note: '免费，代价是本地算力与时间。' },

  detect() {
    const cli = findWhisperCli();
    if (!cli) {
      return {
        available: false,
        reason: '没有找到本地 whisper 可执行文件',
        hint: '安装方式二选一：\n'
          + '    brew install whisper-cpp            （macOS，推荐）\n'
          + '    或参考 https://github.com/ggml-org/whisper.cpp 自行编译\n'
          + '  然后下载模型（如 ggml-small.bin，约 466MB）放到 ~/.cache/whisper/。',
        docs: 'https://github.com/ggml-org/whisper.cpp',
      };
    }
    const model = findModel();
    if (!model) {
      return {
        available: false,
        reason: `找到了 ${cli}，但没有找到模型文件`,
        hint: '下载模型放到 ~/.cache/whisper/：\n'
          + '    curl -L -o ~/.cache/whisper/ggml-small.bin \\\n'
          + '      https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin\n'
          + '  或用环境变量 WHISPER_MODEL 指定已有模型路径。',
        docs: 'https://github.com/ggml-org/whisper.cpp#models',
      };
    }
    if (!which('ffmpeg')) {
      return {
        available: false,
        reason: '本地后端需要 ffmpeg 抽音频，但没有找到',
        hint: 'brew install ffmpeg',
        docs: 'https://ffmpeg.org/',
      };
    }
    return { available: true, detail: `${cli} + ${model}`, cli, model };
  },

  /** 本地后端只接受文件。上层负责先把媒体下下来。 */
  async transcribe({ cli, model, file, language = 'zh', hotwords = [], corpusText = null,
    workDir = '/tmp', onProgress = () => {} }) {
    if (!file || !existsSync(file)) throw new Error('local: 需要提供本地文件路径');

    // 抽成 16kHz 单声道 WAV —— whisper.cpp 只吃这个规格
    const wav = join(workDir, `sph-local-${Date.now()}.wav`);
    onProgress('ffmpeg 抽取 16kHz 单声道音频…');
    const ff = spawnSync(which('ffmpeg') ?? 'ffmpeg', [
      '-y', '-v', 'error', '-i', file,
      '-vn', '-ac', '1', '-ar', '16000', '-c:a', 'pcm_s16le', wav,
    ], { encoding: 'utf8' });
    if (ff.status !== 0 || !existsSync(wav)) {
      throw new Error(`local: 抽音频失败 ${ff.stderr?.slice(0, 300)}`);
    }

    try {
      onProgress('本地推理中（首次会很慢）…');
      const outPrefix = join(workDir, `sph-local-${Date.now()}`);
      const args = ['-m', model, '-f', wav, '-l', language === 'zh' ? 'zh' : 'auto',
        '-otxt', '-of', outPrefix, '--no-prints'];
      // whisper.cpp 没有原生热词，用初始提示词（--prompt）近似引导。
      // 注意它有 ~224 token 上限，超出会被截断，所以这里硬截一刀。
      if (corpusText || hotwords?.length) {
        const prompt = (corpusText || hotwords.join('、')).slice(0, 400);
        args.push('--prompt', prompt);
        onProgress(`携带初始提示词（${prompt.length} 字符，近似热词）`);
      }
      const r = spawnSync(cli, args, { encoding: 'utf8', timeout: 60 * 60 * 1000 });
      const txtPath = `${outPrefix}.txt`;
      if (r.status !== 0 && !existsSync(txtPath)) {
        throw new Error(`local: 推理失败 ${(r.stderr || '').slice(0, 300)}`);
      }
      const text = readFileSync(txtPath, 'utf8').trim();
      if (!text) throw new Error('local: 推理结果为空');
      return { text, model: `whisper:${model.split('/').pop()}`, sentences: [], raw: null };
    } finally {
      rmSync(wav, { force: true });
    }
  },
};
