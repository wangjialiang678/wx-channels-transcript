#!/usr/bin/env node
/**
 * sph-transcript —— 视频号链接 → 逐字稿
 *
 * 设计原则：
 *   · 零 npm 依赖（只用 Node 22+ 内置能力：fetch / node:sqlite / node:crypto）
 *   · 优先云端 ASR（快、准、便宜），本地模型作兜底
 *   · **每一步按需触发**：能直链直传就不下载，能不吃音频就不调 ffmpeg
 *   · 默认零中间文件；要留音频/视频得显式开口
 *
 * 用法：
 *   sph scan                                    扫描本机可用的 ASR 后端
 *   sph <视频号链接>                              提取逐字稿（最常用）
 *   sph <链接> --save-audio                      顺便把音频留下来
 *   sph <链接> --save-audio --audio-format mp3   指定音频格式
 *   sph <链接> -t volcengine --model 2.0         指定后端
 *   sph auth                                     查看登录态缓存
 *   sph backends                                 列出所有支持的后端
 */

import { existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { loadEnv, existingCredentialFiles } from '../src/env.mjs';
import { YuanbaoSession } from '../src/session.mjs';
import { resolve, parseShareId } from '../src/resolve.mjs';
import { detectAll, recommend, getBackend } from '../src/asr/index.mjs';
import { writeOutputs, slugify } from '../src/output.mjs';
import { prepareMedia, promote, cleanup, cleanupDir, hasFfmpeg, AUDIO_FORMATS } from '../src/media.mjs';
import { extractHotwords, parseHotwordsArg, buildCorpusText } from '../src/hotwords.mjs';

const VERSION = '0.3.0';

// ─────────────────────────────────────────────── 参数解析（手写，不引依赖）
const argv = process.argv.slice(2);
const flags = {};
const positional = [];
const VALUE_FLAGS = new Set(['--out', '-o', '--to', '-t', '--model', '-m',
  '--cookie', '--browser', '--lang', '-l', '--audio-format',
  '--hotwords', '--vocabulary-id']);

for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  if (VALUE_FLAGS.has(a)) {
    flags[a.replace(/^--?/, '')] = argv[++i];
  } else if (a.startsWith('--')) {
    flags[a.slice(2)] = true;
  } else if (a === '-h') {
    flags.help = true;
  } else if (a === '-v') {
    flags.version = true;
  } else {
    positional.push(a);
  }
}

const log = (m) => process.stderr.write(`${m}\n`);
const out = (m) => process.stdout.write(`${m}\n`);
const fmtSteps = (steps) => (steps.length === 0 ? '零额外步骤' : `需要：${steps.join(' → ')}`);

// ─────────────────────────────────────────────── help / version
if (flags.help || (positional.length === 0 && !Object.keys(flags).length)) {
  out(`sph-transcript v${VERSION} —— 视频号链接 → 逐字稿

用法:
  sph scan                            扫描本机可用的 ASR 后端，并给出推荐
  sph <视频号链接>                     提取逐字稿
  sph backends                        列出所有支持的后端（含未配置的）
  sph auth                            查看登录态缓存
  sph auth --clear                    清除登录态缓存

输出选项:
  -o, --out <目录>        输出目录（默认 ./transcripts）
      --save-audio        保留音频文件到输出目录
      --save-video        保留原始视频到输出目录
      --audio-format <f>  音频格式：wav（默认）/ mp3 / m4a
      --keep              保留全部中间文件（等价于上面两个都开）

转写选项:
  -t, --to <后端>         指定 ASR 后端：bailian（默认）/ volcengine / local
  -m, --model <模型>      指定模型（各后端可用值见 sph backends）
  -l, --lang <语言>       语言提示，默认 zh

热词选项（提升专名识别率）:
      --hotwords <词表>   额外热词，逗号分隔，或给一个文件路径（每行一个）
      --no-auto-hotwords  关闭「从视频标题自动提取热词」（默认开启）
      --vocabulary-id <id> 阿里侧热词表 ID（需先在百炼控制台创建）
      --show-hotwords     只打印会自动提取的热词，不执行转写

登录态选项:
      --cookie <字符串>    手工提供元宝 cookie（完全跳过钥匙串授权）
      --browser <名称>     指定从哪个浏览器读：chrome / edge / brave / arc …
      --no-browser        禁止读取浏览器（只用缓存或 --cookie）

其它:
      --probe             只解析拿直链，不做转写
      --json              输出 JSON（便于 Agent 解析）
  -h, --help              显示帮助
  -v, --version           显示版本

说明:
  · 视频号解析需要元宝（yuanbao.tencent.com）的登录态，首次会弹一次
    macOS 钥匙串授权；之后会缓存复用，约一个月内不用再授权。
  · 不装证书、不改系统代理、不需要管理员权限。
  · **默认不产生任何中间文件**。走阿里云后端时连下载和 ffmpeg 都不需要。`);
  process.exit(0);
}
if (flags.version) { out(VERSION); process.exit(0); }

const env = loadEnv();

// ─────────────────────────────────────────────── backends
if (positional[0] === 'backends') {
  const detected = detectAll(env);
  out('本工具支持的 ASR 后端：\n');
  for (const d of detected) {
    out(`${d.available ? '✅' : '❌'} ${d.id.padEnd(11)} ${d.label}`);
    out(`   ${d.available ? `已就绪：${d.detail ?? ''}` : `未配置：${d.reason}`}`);
    out(`   特性：直链直传=${d.backend.acceptsUrl ? '是' : '否'}　直接吃mp4=${d.backend.acceptsMp4 ? '是' : '否'}　${fmtSteps(d.extraSteps)}`);
    out(`   模型：${d.backend.models.join(' / ')}`);
    if (d.hint) out(`   如何配置：\n${d.hint.split('\n').map((l) => '     ' + l).join('\n')}`);
    if (d.docs) out(`   官方文档：${d.docs}`);
    out('');
  }
  process.exit(0);
}

// ─────────────────────────────────────────────── scan
if (positional[0] === 'scan') {
  const ff = hasFfmpeg();
  const detected = detectAll(env);
  const { chosen, ranked, reason } = recommend(detected, { hasFfmpeg: ff });

  out('本机能力扫描\n' + '─'.repeat(52));
  out(`ffmpeg：${ff ? '✅ 已安装' : '❌ 未安装'}`);
  if (!ff) out('     （只有「需要本地抽音频」的后端才用得上它；走阿里云时完全不需要）');
  out(`凭据文件：${existingCredentialFiles().map((f) => f.replace(process.env.HOME, '~')).join('、') || '（无）'}`);
  out('');

  for (const d of detected) {
    out(`${d.available ? '✅' : '❌'} ${d.label}`);
    if (d.available) out(`     ${d.detail ?? ''}　${fmtSteps(d.extraSteps)}`);
    else out(`     ${d.reason}`);
  }
  out('');

  if (!chosen) {
    out('⚠️ 没有任何可用的 ASR 后端，无法转写。\n');
    out('最快的解决办法（任选其一）：');
    for (const d of detected.filter((x) => !x.available)) {
      out(`  · ${d.label}：${d.hint?.split('\n')[0] ?? ''}`);
      if (d.docs) out(`    文档：${d.docs}`);
    }
    process.exit(2);
  }

  out('─'.repeat(52));
  out(`推荐：${chosen.label}`);
  out('');
  out(reason.replace(/。([^。])/g, '。\n$1'));
  out('');
  if (flags.json) {
    out(JSON.stringify({
      ffmpeg: ff,
      ranked: ranked.map((r) => ({ id: r.id, label: r.label, score: r.score, notes: r.notes })),
      chosen: chosen.id, reason,
    }, null, 2));
  }
  process.exit(0);
}

// ─────────────────────────────────────────────── auth
if (positional[0] === 'auth') {
  const s = new YuanbaoSession();
  if (flags.clear) {
    s.clearCache();
    out(`已清除登录态缓存：${s.path}`);
    process.exit(0);
  }
  const cached = s.loadCache();
  if (!cached) {
    out('没有登录态缓存。下次提取时会自动读取浏览器并弹一次钥匙串授权。');
    process.exit(0);
  }
  const ageDays = ((Date.now() - new Date(cached.savedAt).getTime()) / 86400000).toFixed(1);
  out(`登录态缓存：${s.path}`);
  out(`保存时间：${cached.savedAt}（${ageDays} 天前）`);
  out(`cookie 条数：${Object.keys(cached.jar).length}`);
  out('');
  out('校验中…');
  const header = Object.entries(cached.jar).map(([k, v]) => `${k}=${v}`).join('; ');
  const v = await s.verify(header);
  out(v.ok ? '✅ 仍然有效' : `❌ 已失效（HTTP ${v.status}），下次会自动重新读取浏览器`);
  process.exit(0);
}

// ─────────────────────────────────────────────── 主流程
const input = positional[0];
if (!input) { log('错误：没有给出链接或文件。用 -h 看用法。'); process.exit(1); }

// 输入可以是「视频号链接」或「本地媒体文件」
const isUrl = /^https?:\/\//i.test(input);
const localFile = isUrl ? null : input;
if (isUrl && !parseShareId(input)) {
  log(`错误：这不是可识别的视频号链接：${input}`);
  log('  期望形如 https://weixin.qq.com/sph/xxxx');
  process.exit(1);
}
if (!isUrl) {
  if (!existsSync(localFile)) {
    log(`错误：既不是 http(s) 链接，本地也找不到这个文件：${localFile}`);
    process.exit(1);
  }
}

// 输出相关选项
const outDir = flags.out || flags.o || join(process.cwd(), 'transcripts');
const wantAudio = !!(flags['save-audio'] || flags.keep);
const wantVideo = !!(flags['save-video'] || flags.keep);
const audioFormat = flags['audio-format'] || 'wav';
if (!AUDIO_FORMATS[audioFormat]) {
  log(`错误：不支持的音频格式 ${audioFormat}。可选：${Object.keys(AUDIO_FORMATS).join(' / ')}`);
  process.exit(1);
}

const t0 = Date.now();
const timing = { resolve: 0, transcribe: 0, media: 0, total: 0 };
// 中间文件放系统临时目录，保证默认零污染
const workDir = join(tmpdir(), `sph-${Date.now()}`);
const tmpPaths = [];

let info;
let session = null;

if (isUrl) {
  // ── 链接路径：需要登录态 + 解析
  session = new YuanbaoSession();
  log('① 获取元宝登录态…');
  try {
    const got = await session.acquire({
      cookie: flags.cookie || null,
      browser: flags.browser || null,
      allowBrowser: !flags['no-browser'],
      onNotice: (msg) => log(msg),
    });
    log(got.origin === 'cache' ? '   使用缓存的登录态（未触碰浏览器）'
      : got.origin === 'arg' ? '   使用 --cookie 提供的登录态'
        : `   已从 ${got.browser} 读取（${got.count} 条 cookie）并缓存`);
  } catch (e) {
    log(`\n✗ ${e.message}`);
    process.exit(3);
  }

  log('② 解析视频号链接…');
  const tR = Date.now();
  try {
    const r = await resolve(input, session.cookie);
    info = { ...r, url: input };
  } catch (e) {
    if (e.isAuth) {
      session.clearCache();
      log(`\n✗ ${e.message}`);
      log('  登录态已失效，缓存已清除。重跑一次会自动重新读取浏览器。');
      process.exit(3);
    }
    log(`\n✗ ${e.message}`);
    process.exit(4);
  }
  timing.resolve = (Date.now() - tR) / 1000;
  log(`   ✓ ${info.title || '(无标题)'}${info.author ? `　@${info.author}` : ''}`);
} else {
  // ── 本地文件路径：不需要登录态，也不需要解析
  const base = localFile.split('/').pop() || 'local-media';
  info = {
    url: localFile, shareId: null, exportId: null,
    title: base.replace(/\.[^.]+$/, ''), author: null, mediaUrl: null,
    isLocal: true, localPath: localFile,
  };
  log(`① 本地文件模式：${base}（跳过登录态与解析）`);
}

if (flags.probe) {
  if (isUrl) {
    const payload = { title: info.title, author: info.author, shareId: info.shareId, exportId: info.exportId, mediaUrl: info.mediaUrl };
    out(flags.json ? JSON.stringify(payload, null, 2) : info.mediaUrl);
  } else {
    out(flags.json ? JSON.stringify({ isLocal: true, localPath: localFile }, null, 2) : localFile);
  }
  process.exit(0);
}

// 2.5) 热词：默认从视频标题里自动提取（标题往往就写着关键专名）
const autoHot = flags['no-auto-hotwords'] ? [] : extractHotwords(info.title);
const extraHot = await parseHotwordsArg(flags.hotwords);
const hotwords = [...new Set([...autoHot, ...extraHot])];

if (flags['show-hotwords']) {
  out(JSON.stringify({
    fromTitle: autoHot,
    fromArg: extraHot,
    merged: hotwords,
  }, null, 2));
  process.exit(0);
}
if (hotwords.length) {
  const src = autoHot.length && extraHot.length ? '标题+参数'
    : autoHot.length ? '视频标题' : '--hotwords 参数';
  log(`   ✓ 热词 ${hotwords.length} 个（来自${src}）：${hotwords.slice(0, 10).join('、')}${hotwords.length > 10 ? '…' : ''}`);
}

// 3) 选后端
const ff = hasFfmpeg();
const detected = detectAll(env);
const { chosen } = recommend(detected, { hasFfmpeg: ff });
let backend = chosen?.backend;
if (flags.to || flags.t) {
  backend = getBackend(flags.to || flags.t);
  const d = detected.find((x) => x.id === backend.id);
  if (!d?.available) {
    log(`\n✗ 指定的后端 ${backend.id} 在本机不可用：${d?.reason ?? '未知原因'}`);
    log('  用 sph backends 看各后端的配置方法。');
    process.exit(5);
  }
}
if (!backend) {
  log('\n✗ 本机没有可用的 ASR 后端，无法转写。');
  log('  跑 sph scan 看缺什么、怎么配。');
  process.exit(5);
}

// 4) 决定是否需要本地媒体 —— 这是「按需」的关键
const backendInfo = backend.detect(env);
const needDownloadForAsr = !backend.acceptsUrl;   // 后端不接受直链 → 必须下载
const needAudioForAsr = !backend.acceptsMp4;      // 后端不吃 mp4 → 必须抽音频
const needLocalMedia = needDownloadForAsr || needAudioForAsr || wantAudio || wantVideo;

log(`③ 转写（${backend.label}）…`);
if (!needDownloadForAsr && !needAudioForAsr) {
  log('   该后端支持直链直传且能直接处理 mp4 —— 不下载、不转码');
} else if (needAudioForAsr && !needDownloadForAsr) {
  log('   该后端不吃 mp4 —— 需要先抽音频');
} else {
  log('   该后端只接受本地文件 —— 需要先下载');
}
if (wantAudio || wantVideo) {
  log(`   另外你要求保留${wantAudio ? `音频(${audioFormat})` : ''}${wantAudio && wantVideo ? '和' : ''}${wantVideo ? '原始视频' : ''}`);
}

let local = { mediaPath: null, audioPath: null };
if (info.isLocal) {
  // 本地文件模式下「媒体」已经在用户手上（就是输入文件本身），
  // 不存在"下载"这一步——早先这里会无条件调 prepareMedia(info.mediaUrl)，
  // 而本地模式下 mediaUrl 是 null，直接 fetch(null) 崩掉。
  local.mediaPath = info.localPath;
  if (wantAudio) {
    const { extractAudio } = await import('../src/media.mjs');
    const { join: pjoin } = await import('node:path');
    const spec = AUDIO_FORMATS[audioFormat] ?? AUDIO_FORMATS.wav;
    const dest = pjoin(workDir, `audio-${Date.now()}.${spec.ext}`);
    try {
      extractAudio(info.localPath, dest, {
        format: audioFormat, forAsr: audioFormat === 'wav',
        onProgress: (m) => log(`   ${m}`),
      });
      local.audioPath = dest;
      tmpPaths.push(dest);
    } catch (e) {
      log(`   ⚠️ 抽取音频失败（不影响转写）：${e.message}`);
    }
  }
} else if (needLocalMedia) {
  const tM = Date.now();
  try {
    local = await prepareMedia(info.mediaUrl, {
      workDir,
      needDownload: needDownloadForAsr || wantVideo || needAudioForAsr || wantAudio,
      needAudio: needAudioForAsr || wantAudio,
      audioFormat,
      onProgress: (m) => log(`   ${m}`),
    });
    for (const p of [local.mediaPath, local.audioPath]) if (p) tmpPaths.push(p);
  } catch (e) {
    log(`\n✗ 准备媒体失败：${e.message}`);
    cleanupDir(workDir);
    process.exit(6);
  }
  timing.media = (Date.now() - tM) / 1000;
}

// 5) 转写
const tT = Date.now();
let transcript;
try {
  const onProgress = (m) => log(`   ${m}`);
  // 热词统一从这里传，各后端按自己的能力取用：
  //   阿里   → parameters.corpus.text（任意文本，实测有效，无需预建词表）
  //   火山   → corpus.context 里的 hotwords 数组
  //   本地   → whisper 初始提示词
  const hotOpts = {
    hotwords,
    corpusText: buildCorpusText(hotwords),
    vocabularyId: flags['vocabulary-id'] || null,
  };

  if (info.isLocal) {
    // 本地文件：不走下载，直接把文件交给后端。
    // ⚠️ 阿里支持上传本地文件（临时上传通道）；火山只接受公网 URL，会明确报错。
    transcript = await backend.transcribe({
      ...backendInfo, ...hotOpts, url: null, file: info.localPath,
      model: flags.model || flags.m, language: flags.lang || flags.l || 'zh', onProgress,
    });
  } else if (backend.acceptsUrl && !needAudioForAsr) {
    // 最快路径：直链直传
    transcript = await backend.transcribe({
      ...backendInfo, ...hotOpts, url: info.mediaUrl, file: null,
      model: flags.model || flags.m, language: flags.lang || flags.l || 'zh', onProgress,
    });
  } else if (backend.acceptsUrl && local.audioPath) {
    // 有直链但后端要音频格式：把刚抽好的音频传上去
    transcript = await backend.transcribe({
      ...backendInfo, ...hotOpts, url: null, file: local.audioPath,
      model: flags.model || flags.m, language: flags.lang || flags.l || 'zh', onProgress,
    });
  } else {
    // 本地后端
    transcript = await backend.transcribe({
      ...backendInfo, ...hotOpts, file: local.audioPath || local.mediaPath,
      model: flags.model || flags.m || backendInfo.model,
      language: flags.lang || flags.l || 'zh', workDir, onProgress,
    });
  }
} catch (e) {
  log(`\n✗ 转写失败：${e.message}`);
  cleanupDir(workDir);
  process.exit(7);
}
timing.transcribe = (Date.now() - tT) / 1000;
timing.total = (Date.now() - t0) / 1000;

// 6) 落盘
const stamp = new Date().toISOString().slice(0, 10);
const baseName = `${stamp}-${slugify(info.title, info.shareId || 'transcript')}`;
// 时长优先用 ASR 返回的（从分句时间戳推得），其次用解析阶段的
if (!info.duration && transcript.durationSec) info.duration = transcript.durationSec;
const { mdPath, jsonPath, textLength } = writeOutputs({ outDir, info, transcript, backend, timing });

// 7) 按需保留媒体
const kept = [];
if (wantAudio && local.audioPath) {
  const p = promote(local.audioPath, outDir, baseName);
  if (p) kept.push(p);
}
if (wantVideo && local.mediaPath) {
  const p = promote(local.mediaPath, outDir, baseName);
  if (p) kept.push(p);
}

// 8) 清理临时文件（默认零残留）
if (!flags.keep) cleanup(tmpPaths);
cleanupDir(workDir);

log('');
const mediaPart = timing.media ? ` + 媒体 ${timing.media.toFixed(1)}s` : '';
log(`✓ 完成：${textLength} 字，解析 ${timing.resolve.toFixed(1)}s${mediaPart} + 转写 ${timing.transcribe.toFixed(1)}s = ${timing.total.toFixed(1)}s`);
log(`  逐字稿：${mdPath}`);
log(`  JSON  ：${jsonPath}`);
for (const k of kept) log(`  已保留：${k}`);
if (flags.json) {
  out(JSON.stringify({
    ok: true, mdPath, jsonPath, backend: backend.id, model: transcript.model,
    textLength, timing, kept,
  }, null, 2));
} else {
  out(mdPath);
}
