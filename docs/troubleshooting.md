# 故障排查

## 先做这两步

```bash
wx-channels-transcript scan      # 本机能走哪条路、缺什么
wx-channels-transcript auth      # 登录态还有效吗
```

90% 的问题这两条命令就能定位。

---

## 平台相关

### Windows / Linux 上提示「自动读取浏览器登录态目前只支持 macOS」

**这不是 bug，是功能边界。**

自动读取之所以只做了 macOS，是因为三家的 cookie 加密方式完全不同：

| 平台 | cookie 加密 | 状态 |
|---|---|---|
| macOS | 钥匙串（Chrome Safe Storage）+ AES-128-CBC | ✅ 已实现 |
| Windows | Chrome 127+ 起用 **App-Bound Encryption**——密钥受 elevation 服务保护，需要调 `IElevator` COM 接口才能解 | ❌ 是另一套工程 |
| Linux | gnome-keyring / keyring 实现各异 | ❌ 未实现 |

**工具本身完全可用**，只是要手工提供一次登录态：

```bash
# 1. 用浏览器打开 https://yuanbao.tencent.com 并扫码登录
# 2. F12 → Application（存储）→ Cookies → 选中 yuanbao.tencent.com
# 3. 把该域名下的 cookie 全部复制成 "name=value; name2=value2" 的形式
# 4. 运行：
wx-channels-transcript "<链接>" --cookie "hy_token=xxx; hy_user=yyy; ..."
```

**几个要点**：
- 元宝的会话 cookie 实测挂在**父域 `.tencent.com`** 下，不只是 `yuanbao.tencent.com`——复制时注意别漏
- 登录态有效期约一个月，过期后重新复制一次即可
- 加 `--no-browser` 可以彻底禁止触碰浏览器（避免在 macOS 之外的环境里反复尝试）

**想彻底不碰浏览器**：也可以用 `-t local` 走本地 whisper 模型，全程离线（但仍需 cookie 才能解析视频号链接——因为那是腾讯侧的鉴权）。

---

## 登录态相关

### 报「在 chrome 里没有找到 yuanbao.tencent.com 的 cookie」

**原因**：这个浏览器里从没登录过元宝。

**处理**：用该浏览器打开 https://yuanbao.tencent.com ，微信扫码登录，然后重跑。

### 报「读取到的 cookie 无法通过元宝校验（HTTP 401）」

**原因**：登录态过期了（约一个月会过期一次）。

**处理**：
```bash
wx-channels-transcript auth --clear    # 清掉缓存
wx-channels-transcript "<链接>"         # 重跑，会自动重新读取浏览器
```

### 弹了两次密码框

**原因**：**是两个不同的程序各自申请了一次**。macOS 的钥匙串授权是按「申请方二进制」分别记住的——比如之前的工具用 Python（yt-dlp），本工具用 Node，两边互不认账。

**处理**：只用本工具的话只会弹一次。都点「始终允许」之后，两边的授权都会记住。

### 不想授权钥匙串

三个选择：

1. **手工粘贴 cookie**（推荐，零弹窗）
   1. Chrome 打开 `https://yuanbao.tencent.com`
   2. F12 → Application → Cookies → 复制全部的 `name=value`，用 `;` 连起来
   3. `sph "<链接>" --cookie "hy_token=xxx; hy_user=yyy; ..."`

2. **换 Firefox 登录元宝** —— Firefox 的 cookie 不加密，理论上无需授权（本工具目前未实现该路径）

3. **`--no-browser`** —— 只用缓存或 `--cookie`，完全禁止触碰浏览器

### 钥匙串弹窗里到底该输什么

**Mac 登录密码**（开机/解锁电脑的那个）。不是元宝密码，不是 Apple ID 密码，不是微信密码。

那个对话框是 macOS 系统的，不是本程序的——系统在问「这个程序要拿走 Chrome 的加密密钥，你同意吗」。

---

## 解析相关

### 报「拿不到 export_id」

服务端明确告诉你这条视频**不可用**：已删除、私密、或分享链接过期。换一条链接即可。这不是程序 bug。

### 报「该链接不受支持（直播 / 回放 / 已下架等）」

**视频号直播和回放不支持**。元宝接口只认已发布的视频。

需要直播，得用 MITM 抓包方案：
- [wx_channels_download](https://github.com/ltaoo/wx_channels_download)
- [putyy/res-downloader](https://github.com/putyy/res-downloader)

它们需要管理员权限 + 装根证书 + 起本地代理，与本工具路线不同。

### 报「这不是可识别的视频号链接」

支持的形态：
```
https://weixin.qq.com/sph/xxxxxxxx
https://channels.weixin.qq.com/finder-preview/pages/sph?id=xxxxxxxx
xxxxxxxx            （直接粘 shareId 也认）
```

---

## 转写相关

### 报「没有找到 chromium 系浏览器」

本工具的浏览器读取只实现了 macOS + Chromium 家族（Chrome / Edge / Brave / Arc / Chromium / Vivaldi / Opera）。

**处理**：用 `--cookie` 手工提供，或者到装了 Chrome 的机器上跑。

### 报需要 ffmpeg

你选的（或自动推荐的）后端**不能直接吃 mp4**，需要先抽音频。

**处理**：
- **推荐**：改用 `-t bailian`（阿里云能直接吃 mp4，完全不需要 ffmpeg）
- 或 `brew install ffmpeg`

### 报「任务失败 / 轮询超时」

- **阿里云**：检查 Key 是否有效、是否开通了对应模型、账户余额
- **火山**：检查 `X-Api-Resource-Id` 对应的模型是否已开通
- 音频超长（阿里云上限 12 小时 / 2GB，火山单文件 < 512MB）

### 转写结果里有错字

**正常**。ASR 是统计模型，同音字、人名、英文专名、数字都容易出错。

**要精确引用（写稿、引原话）必须回原视频核对。**
做播客类内容时，用「热词 / 上下文」功能能显著改善（阿里云和火山都支持），本工具预留了扩展位但尚未实现。

---

## 环境相关

### 输出里有 `NODE_TLS_REJECT_UNAUTHORIZED=0` 警告

你的环境里设了这个变量（通常是代理工具或某些 CLI 留下的）。它让 Node **跳过所有 HTTPS 证书校验**。

⚠️ **这不是"无害"的**——本工具正带着**元宝的登录态 cookie** 和**音频内容**在网络上通信，跳过证书校验意味着这些数据可能被中间人读取。本工具自身没有任何防护。

**建议**：查出是谁设的，从 shell 配置里去掉它；如果是为了代理而必须设，请确保你只在自己信任的网络环境下使用。

### 中间文件

**默认不产生任何中间文件**——工作目录用系统临时目录，跑完（无论成功失败）都会删除。

如果你看到残留，可能是进程被强杀（`kill -9`）导致的，可以手动清理 `/tmp/sph-*`。

另外：从 Chrome 读取 cookie 时会在临时目录里放一份 cookie 库副本，**用完即删**（`src/chrome-cookies.mjs` 用 `finally` 保证）。如果你发现 `/tmp/sph-cookies-*` 有残留，那是异常中断留下的，可以直接删掉。

---

## 还是不行？

收集这些信息再反馈：

```bash
node --version
wx-channels-transcript scan
wx-channels-transcript auth
```

以及完整的报错输出（stderr 里带步骤编号 ①②③ 的部分）。
