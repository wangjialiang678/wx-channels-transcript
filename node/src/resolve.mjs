/**
 * 视频号分享链接 → 可下载的媒体直链。
 *
 * 三步走（每一步都在真实会话上验证过）：
 *   1. POST /api/weixin/get_parse_result   分享链接 → wx_export_id
 *   2. POST /api/findergetobjecturl        wx_export_id → 直链
 *   3. 直链自带 token+sign 签名，下载时不需要任何 cookie，也不需要解密
 *
 * 注意：这是元宝（腾讯自家的 AI 助手）对外暴露的接口，属于**非公开接口**。
 * 腾讯随时可能调整字段或加风控，代码里对字段名做了多重兜底
 * （见 pick()），但仍然要预期它会失效。
 */

export const YUANBAO = 'https://yuanbao.tencent.com';
const PARSE_URL = `${YUANBAO}/api/weixin/get_parse_result`;
const OBJECT_URL = `${YUANBAO}/api/findergetobjecturl`;
const REQUEST_TIMEOUT_MS = 15000;

const BASE_HEADERS = {
  Accept: 'application/json, text/plain, */*',
  'X-Source': 'web',
  Origin: YUANBAO,
  Referer: `${YUANBAO}/`,
  'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) '
    + 'AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15',
};

/** 已知的两种分享链接形态；返回 shareId 或 null。 */
export function parseShareId(url) {
  const s = String(url).trim();
  let m = /weixin\.qq\.com\/sph\/([A-Za-z0-9_-]+)/.exec(s);
  if (m) return m[1];
  m = /channels\.weixin\.qq\.com\/[^?#]*\?(?:[^#]*&)?id=([A-Za-z0-9_-]+)/.exec(s);
  if (m) return m[1];
  m = /^([A-Za-z0-9_-]{6,})$/.exec(s); // 用户直接粘 shareId 也认
  if (m) return m[1];
  return null;
}

/** 从多个候选字段名里取第一个非空字符串——接口字段改名时不至于直接挂掉。 */
function pick(obj, keys) {
  if (!obj || typeof obj !== 'object') return undefined;
  for (const k of keys) {
    const v = obj[k];
    if (typeof v === 'string' && v.length > 0) return v;
  }
  return undefined;
}

/** 拆掉常见的 { data: {...} } 信封。 */
function unwrap(json) {
  if (json && typeof json === 'object' && json.data && typeof json.data === 'object') return json.data;
  return json ?? {};
}

async function callApi(url, cookie, body) {
  const headers = { ...BASE_HEADERS, Cookie: cookie };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const res = await fetch(url, {
    method: body === undefined ? 'GET' : 'POST',
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
  });
  const text = await res.text();
  let json = null;
  try { json = text ? JSON.parse(text) : null; } catch { /* 非 JSON 响应 */ }
  return { httpStatus: res.status, json };
}

/** 把业务错误归类，便于上层给出不同的重试建议。 */
function classify(httpStatus, json) {
  if (httpStatus === 401 || httpStatus === 403) return 'auth';
  if (httpStatus === 404) return 'not_found';
  const msg = pick(json?.error, ['message']) || pick(json, ['message', 'msg']) || '';
  if (/^success$/i.test(msg.trim())) return null;
  if (!msg) return null;
  if (/(get\s*token\s*err|登录|未登录|unauthorized|invalid\s*session|请先登录|重新登录)/i.test(msg)) return 'auth';
  if (/(直播|live\b|回放|replay|不支持|unsupported|已下架|已删除|removed|deleted|违规)/i.test(msg)) return 'unsupported';
  if (/(不存在|not\s*found|无效|已过期|失效|404)/i.test(msg)) return 'not_found';
  return null;
}

function looksLikeMediaUrl(s) {
  if (!/^https?:\/\//i.test(s)) return false;
  if (/\.(jpe?g|png|gif|webp|bmp|svg|ico)(\?|$)/i.test(s)) return false;
  // finder.video.qq.com/.../stodownload?... 没有扩展名，所以还要按主机名判断
  return /\.(mp4|m3u8|ts|mov|m4v|flv)(\?|$)/i.test(s)
    || /(finder\.video\.qq\.com|\.video\.qq\.com|\.tc\.qq\.com|mmfinder)/i.test(s);
}

/**
 * 解析一条视频号链接。
 * @param {string} url       分享链接（或直接给 shareId）
 * @param {string} cookie    元宝登录态的 Cookie 头
 * @returns {Promise<{shareId, exportId, title, author, mediaUrl}>}
 */
export async function resolve(url, cookie) {
  const shareId = parseShareId(url);
  if (!shareId) {
    throw new Error(`这不是可识别的视频号链接：${url}\n`
      + '  支持的形态：https://weixin.qq.com/sph/xxxx\n'
      + '               https://channels.weixin.qq.com/finder-preview/pages/sph?id=xxxx');
  }

  // 步骤 1：分享链接 → export id
  const p = await callApi(PARSE_URL, cookie, { type: 'video_channel_url', url, scene: 1 });
  const pErr = classify(p.httpStatus, p.json);
  if (pErr === 'auth') throw new AuthError('元宝登录态在解析过程中被拒绝');
  if (pErr === 'unsupported') throw new Error('该链接不受支持（直播 / 回放 / 已下架等）');
  if (pErr === 'not_found') throw new Error('视频不存在或已失效');

  const d = unwrap(p.json);
  const exportId = pick(d, ['wx_export_id', 'exportId', 'export_id']);
  if (!exportId) {
    throw new Error('拿不到 export_id —— 该视频可能已删除、私密或过期。\n'
      + `  服务端返回：${JSON.stringify(p.json).slice(0, 300)}`);
  }
  const title = pick(d, ['desc', 'title', 'objectDesc']) || '';
  const author = pick(d, ['author', 'nickname', 'authorName']) || '';

  // 步骤 2：export id → 直链
  // 注意 exportId 必须是「字符串」；传数组会返回业务码 500
  const o = await callApi(OBJECT_URL, cookie, { exportId });
  const oErr = classify(o.httpStatus, o.json);
  if (oErr === 'auth') throw new AuthError('元宝登录态在换取直链时被拒绝');
  const od = unwrap(o.json);
  const mediaUrl = pick(o.json, ['videoUrl', 'video_url', 'url'])
    || pick(od, ['videoUrl', 'video_url', 'url']);
  if (!mediaUrl || !looksLikeMediaUrl(mediaUrl)) {
    throw new Error('拿到了 export_id，但换不到媒体直链。\n'
      + `  服务端返回：${JSON.stringify(o.json).slice(0, 300)}`);
  }

  return { shareId, exportId, title, author, mediaUrl };
}

/** 登录态类错误单独标出来，便于上层决定「要不要重新授权」。 */
export class AuthError extends Error {
  constructor(message) {
    super(message);
    this.name = 'AuthError';
    this.isAuth = true;
  }
}

/** 顺带拿评论区（可选，接口存在但默认不用——热门视频会很慢）。 */
export async function fetchComments(url, cookie) {
  const p = await callApi(PARSE_URL, cookie, { type: 'video_channel_url', url, scene: 1 });
  return unwrap(p.json);
}
