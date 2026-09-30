/**
 * 读取浏览器中保存的 cookie（目前实现 macOS + Chrome 家族）。
 *
 * 为什么需要它：视频号解析必须带元宝（yuanbao.tencent.com）的登录态，
 * 而这条 cookie 通常已经存在于用户日常使用的浏览器里——直接借用，
 * 用户就不必去开 DevTools 手工复制。
 *
 * macOS 上 Chrome 系浏览器的 cookie 用 AES-128-CBC 加密，主密钥存在
 * 系统钥匙串的 "Chrome Safe Storage" 条目里。因此第一次读取会触发
 * 一次系统授权弹窗——这是 macOS 的安全机制，不是本程序私自索取。
 *
 * ⚠️ 两个必须遵守的实现纪律（都踩过）：
 *   1. 不要按名字挑 cookie。会话 cookie 未必叫 hy_token，改一次名就静默失效。
 *   2. 不要按域名前缀过滤。hy_token / hy_user 实测挂在父域 `.tencent.com` 下，
 *      必须做标准的 cookie 作用域匹配（host-only 精确 / 域后缀匹配）。
 */

import { execFileSync } from 'node:child_process';
import { createDecipheriv, pbkdf2Sync } from 'node:crypto';
import { copyFileSync, existsSync, mkdtempSync, rmSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';
import { DatabaseSync } from 'node:sqlite';

/** Chrome 家族各浏览器的 cookie 库相对路径（macOS）。 */
const CHROMIUM_BROWSERS = {
  chrome: 'Google/Chrome',
  chromium: 'Chromium',
  edge: 'Microsoft Edge',
  brave: 'BraveSoftware/Brave-Browser',
  arc: 'Arc',
  vivaldi: 'Vivaldi',
  opera: 'com.operasoftware.Opera',
};

/** cookie 作用域匹配：host_key 以点开头表示域级 cookie。 */
export function hostMatches(hostKey, requestHost) {
  if (hostKey.startsWith('.')) {
    const d = hostKey.slice(1);
    return requestHost === d || requestHost.endsWith(`.${d}`);
  }
  return requestHost === hostKey;
}

function chromeSupportDir(browser) {
  const rel = CHROMIUM_BROWSERS[browser];
  if (!rel) return null;
  return join(homedir(), 'Library/Application Support', rel);
}

/** 列出本机实际存在的 Chromium 系浏览器（按常见优先顺序）。 */
export function detectChromiumBrowsers() {
  const found = [];
  for (const [name, rel] of Object.entries(CHROMIUM_BROWSERS)) {
    const dir = join(homedir(), 'Library/Application Support', rel);
    if (existsSync(dir)) found.push({ name, dir });
  }
  return found;
}

/** 在浏览器目录下找一个可用的 Cookies 库（新版在 Network/ 子目录）。 */
function findCookieDb(browserDir) {
  const candidates = [
    join(browserDir, 'Default', 'Network', 'Cookies'),
    join(browserDir, 'Default', 'Cookies'),
    join(browserDir, 'Profile 1', 'Network', 'Cookies'),
    join(browserDir, 'Profile 1', 'Cookies'),
  ];
  for (const c of candidates) if (existsSync(c)) return c;
  return null;
}

/**
 * 从钥匙串取 Chrome 的解密主密钥。
 * 第一次调用会弹系统授权窗口——调用方应在此之前向用户解释清楚。
 */
function chromeKey() {
  const password = execFileSync(
    'security',
    ['find-generic-password', '-w', '-s', 'Chrome Safe Storage', '-a', 'Chrome'],
    { encoding: 'utf8' },
  ).trim();
  // Chrome 用的是固定 salt + 1003 轮，不是随机 salt；IV 固定为 16 个空格
  return { key: pbkdf2Sync(password, 'saltysalt', 1003, 16, 'sha1'), iv: Buffer.alloc(16, ' ') };
}

function decryptValue(encrypted, key, iv) {
  const buf = Buffer.from(encrypted);
  const prefix = buf.subarray(0, 3).toString();
  if (prefix !== 'v10' && prefix !== 'v11') return null; // v20(App-Bound) 等暂不支持
  try {
    const d = createDecipheriv('aes-128-cbc', key, iv);
    let plain = Buffer.concat([d.update(buf.subarray(3)), d.final()]);
    // 新版 Chrome 在明文前塞 32 字节 domain hash，按可打印性判断是否剥掉
    if (plain.length > 32 && !/^[\x20-\x7e]+$/.test(plain.subarray(0, 32).toString('latin1'))) {
      plain = plain.subarray(32);
    }
    // 必须用 latin1：有些 cookie 是二进制（实测 analytics 类），
    // utf8 解码会产生 U+FFFD，而 Node 的 fetch 只接受 ByteString，会直接抛错。
    return plain.toString('latin1').replace(/[\x00-\x08\x0a-\x1f\x7f]/g, '');
  } catch {
    return null;
  }
}

/**
 * 读取指定浏览器里、会对 requestHost 生效的全部 cookie。
 * @returns {Promise<{jar: Record<string,string>, browser: string, db: string}>}
 */
export function readBrowserCookies(requestHost, browserName = null) {
  const browsers = detectChromiumBrowsers();
  if (browsers.length === 0) {
    throw new Error('没有找到任何 Chromium 系浏览器（Chrome / Edge / Brave / Arc …）。'
      + '可改用 --cookie 手工粘贴，或换一台已登录的机器。');
  }
  const target = browserName
    ? browsers.find((b) => b.name === browserName)
    : browsers[0];
  if (!target) throw new Error(`没有找到浏览器 ${browserName}`);

  const db = findCookieDb(target.dir);
  if (!db) throw new Error(`${target.name} 下没有找到 Cookies 数据库`);

  const { key, iv } = chromeKey();

  // 浏览器运行时会锁住数据库，复制一份再读。
  // ⚠️ 这份副本里装的是用户的**全部相关 cookie**（含登录态），属于敏感数据，
  // 因此无论是正常结束还是中途抛错，都必须删掉——用 finally 保证。
  const tmpDir = mkdtempSync(join(tmpdir(), 'sph-cookies-'));
  try {
    const tmp = join(tmpDir, 'Cookies');
    copyFileSync(db, tmp);
    const conn = new DatabaseSync(tmp);
    let rows;
    try {
      rows = conn.prepare('SELECT host_key,name,value,encrypted_value FROM cookies').all();
    } finally {
      conn.close();
    }

    const jar = {};
    for (const r of rows) {
      if (!hostMatches(r.host_key, requestHost)) continue;
      let v = r.value;
      if (!v && r.encrypted_value?.length) v = decryptValue(r.encrypted_value, key, iv);
      if (v) jar[r.name] = v;
    }
    return { jar, browser: target.name, db };
  } finally {
    rmSync(tmpDir, { recursive: true, force: true });
  }
}

/** 把 cookie 对象拼成 Cookie 请求头。 */
export function cookieHeader(jar) {
  return Object.entries(jar).map(([k, v]) => `${k}=${v}`).join('; ');
}
