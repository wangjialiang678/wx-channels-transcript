/**
 * 环境变量与凭据加载。
 *
 * 除了常规的 process.env，还支持从几个常见的凭据文件里读——
 * 这样用户不必为了用这个工具而改变自己既有的密钥管理方式。
 */

import { existsSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

/** 默认会尝试读取的凭据文件（存在才读，后者覆盖前者）。 */
export const CREDENTIAL_FILES = [
  join(homedir(), '.claude/api-vault.env'),
  join(homedir(), '.config/sph-transcript/.env'),
  join(homedir(), '.env'),
];

const LINE_RE = /^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/;

function parseEnvFile(path) {
  const out = {};
  if (!existsSync(path)) return out;
  for (const raw of readFileSync(path, 'utf8').split('\n')) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const m = LINE_RE.exec(line);
    if (!m) continue;
    let v = m[2].trim();
    // 剥掉成对的引号。注意要比较「首尾」而不是「前两个字符」——
    // 后者（v[0] === v[1]）永远不成立，是个曾经存在的 bug。
    const q = v[0];
    if (v.length >= 2 && (q === '"' || q === "'") && v[v.length - 1] === q) {
      v = v.slice(1, -1);
    }
    // 跳过明显被停用的占位值
    if (/^(DISABLED|TODO|xxx|your[_-]?)/i.test(v)) continue;
    out[m[1]] = v;
  }
  return out;
}

/**
 * 合并环境：凭据文件在前，真实环境变量覆盖之（后者优先级更高，
 * 这样用户临时 export 一个值就能压过文件里的旧值）。
 */
export function loadEnv(files = CREDENTIAL_FILES) {
  const merged = {};
  for (const f of files) Object.assign(merged, parseEnvFile(f));
  return { ...merged, ...process.env };
}

/** 列出哪些凭据文件实际存在（用于 --scan 输出）。 */
export function existingCredentialFiles(files = CREDENTIAL_FILES) {
  return files.filter((f) => existsSync(f));
}
