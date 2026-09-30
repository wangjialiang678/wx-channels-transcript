/**
 * 元宝（yuanbao.tencent.com）登录态的管理：获取、验证、缓存、续期。
 *
 * 设计取舍：
 *   - **优先用缓存**。拿一次 cookie 存起来，之后不再碰浏览器、不再弹钥匙串。
 *   - **服务端会续期**。getuserinfo 响应里可能带回新的会话 cookie，
 *     必须写回缓存，否则放旧的用不了多久。
 *   - **权限比凭证重要**。这里只存"这个账号的会话"，不存用户的任何其他数据。
 */

import { existsSync, mkdirSync, readFileSync, writeFileSync, chmodSync, unlinkSync } from 'node:fs';
import { dirname } from 'node:path';
import { cookieHeader, readBrowserCookies } from './chrome-cookies.mjs';

export const YUANBAO = 'https://yuanbao.tencent.com';
export const USER_INFO_URL = `${YUANBAO}/api/getuserinfo`;
export const WECHAT_HOST = 'yuanbao.tencent.com';

const DEFAULT_SESSION_PATHS = [
  `${process.env.HOME}/.config/sph-transcript/session.json`,
];

export function defaultSessionPath() {
  return process.env.SPH_SESSION || DEFAULT_SESSION_PATHS[0];
}

/** 弹窗预告 —— 在真正触发钥匙串之前必须先把话说清楚。 */
export const KEYCHAIN_NOTICE = `
────────────────────────────────────────────────────────────
接下来 macOS 可能弹出一个「钥匙串」授权窗口，请先看这里：

  · 它不是你正在用的程序弹的，是 macOS 的安全机制
  · 要输入的是：你的 Mac 登录密码（开机密码）
  · 用途是：解密 Chrome 里保存的元宝登录态
  · 我们只读取 ${WECHAT_HOST} 这一个域名下的 cookie，
    不会读取其他任何网站

  建议点「始终允许」——下次就不再询问。

  不想授权？用 --cookie "<粘贴的cookie>" 可完全跳过这一步。
────────────────────────────────────────────────────────────
`;

export class YuanbaoSession {
  #sessionPath;
  #jar = null;
  #origin = 'none';

  constructor(sessionPath = defaultSessionPath()) {
    this.#sessionPath = sessionPath;
  }

  get origin() { return this.#origin; }
  get cookie() { return this.#jar ? cookieHeader(this.#jar) : null; }
  get path() { return this.#sessionPath; }

  /** 验证一个 cookie 是否可用；返回 {ok, renewed: 名称→值}。 */
  async verify(header) {
    const res = await fetch(USER_INFO_URL, {
      headers: {
        Accept: 'application/json, text/plain, */*',
        'X-Source': 'web',
        Origin: YUANBAO,
        Referer: `${YUANBAO}/`,
        'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) '
          + 'AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15',
        Cookie: header,
      },
      signal: AbortSignal.timeout(15000),
    });
    // 只取续期，不理会删除（服务端会先删 host-only 再写域级，同一响应里两条）
    const renewed = {};
    for (const sc of res.headers.getSetCookie?.() ?? []) {
      const [pair = '', ...attrs] = sc.split(';');
      const i = pair.indexOf('=');
      if (i <= 0) continue;
      const name = pair.slice(0, i).trim();
      const value = pair.slice(i + 1).trim();
      const deleting = attrs.some((a) => /^\s*max-age\s*=\s*(0|-\d+)\s*$/i.test(a));
      if (!value || deleting) continue;
      if (/[\x00-\x08\x0a-\x1f\x7f]/.test(value)) continue;
      renewed[name] = value;
    }
    return { ok: res.status === 200, status: res.status, renewed };
  }

  loadCache() {
    if (!existsSync(this.#sessionPath)) return null;
    try {
      const d = JSON.parse(readFileSync(this.#sessionPath, 'utf8'));
      if (!d?.jar || typeof d.jar !== 'object') return null;
      return d;
    } catch {
      return null;
    }
  }

  saveCache(jar) {
    mkdirSync(dirname(this.#sessionPath), { recursive: true });
    writeFileSync(this.#sessionPath, JSON.stringify({
      savedAt: new Date().toISOString(),
      host: WECHAT_HOST,
      note: '元宝会话 cookie 缓存。由 sph-transcript 自动维护，可直接删除以强制重新授权。',
      jar,
    }, null, 2));
    chmodSync(this.#sessionPath, 0o600);
  }

  clearCache() {
    if (existsSync(this.#sessionPath)) unlinkSync(this.#sessionPath);
  }

  /**
   * 按优先级取得一个可用的登录态：
   *   1. --cookie 显式传入
   *   2. 本地缓存
   *   3. 读浏览器（会弹钥匙串，调用方应先打印 KEYCHAIN_NOTICE）
   *   4. 都失败 → 抛出带指引的错误
   */
  async acquire({ cookie = null, browser = null, allowBrowser = true, onNotice = null } = {}) {
    // 1) 显式传入
    if (cookie) {
      const v = await this.verify(cookie);
      if (!v.ok) {
        throw new Error(`--cookie 传入的登录态无效（getuserinfo 返回 HTTP ${v.status}）。`
          + '请重新从浏览器复制，或去掉 --cookie 让它自动读取。');
      }
      this.#jar = this.#parseHeader(cookie, v.renewed);
      this.#origin = 'arg';
      this.saveCache(this.#jar);
      return { origin: this.#origin };
    }

    // 2) 缓存
    const cached = this.loadCache();
    if (cached) {
      const header = cookieHeader(cached.jar);
      const v = await this.verify(header);
      if (v.ok) {
        this.#jar = { ...cached.jar, ...v.renewed };
        this.#origin = 'cache';
        if (Object.keys(v.renewed).length) this.saveCache(this.#jar);
        return { origin: this.#origin, renewed: Object.keys(v.renewed) };
      }
      // 缓存失效：清掉，继续往下走
      this.clearCache();
    }

    if (!allowBrowser) {
      throw new Error('登录态已失效，且当前不允许读取浏览器。请用 --cookie 重新提供。');
    }

    // 3) 读浏览器
    onNotice?.(KEYCHAIN_NOTICE);
    const { jar, browser: used } = readBrowserCookies(WECHAT_HOST, browser);
    if (Object.keys(jar).length === 0) {
      throw new Error(`在 ${used} 里没有找到 ${WECHAT_HOST} 的 cookie。\n`
        + `  请先用 ${used} 打开 ${YUANBAO} 并扫码登录，然后重试。\n`
        + '  或者用 --cookie 手工提供登录态。');
    }
    const header = cookieHeader(jar);
    const v = await this.verify(header);
    if (!v.ok) {
      throw new Error(`读取到的 cookie 无法通过元宝校验（HTTP ${v.status}）。\n`
        + `  通常是登录态已过期：请用 ${used} 重新打开 ${YUANBAO} 扫码登录。\n`
        + '  （若浏览器里确实已登录，可尝试 --browser 指定其它浏览器）');
    }
    this.#jar = { ...jar, ...v.renewed };
    this.#origin = `browser:${used}`;
    this.saveCache(this.#jar);
    return { origin: this.#origin, count: Object.keys(jar).length, browser: used };
  }

  #parseHeader(header, renewed = {}) {
    const jar = {};
    for (const part of header.split(';')) {
      const i = part.indexOf('=');
      if (i > 0) jar[part.slice(0, i).trim()] = part.slice(i + 1).trim();
    }
    return { ...jar, ...renewed };
  }
}
