# Watcher Security

`watcher` 的安全防护分成两层：

- 内置防护：跟着 `service` 和 `relay` 一起跑，适合本地开发和公网部署时的应用内兜底。
- 公网边界防护：放在反向代理、WAF、主机防火墙这一层，和业务服务解耦，也能复用到别的服务。

安全面与业务模块隔离。模块不能拥有 transport、auth、公网暴露或 secret
handling；边界说明见 [Security Plane](foundation/SECURITY_PLANE.md)。

## 内置防护

现在 `service` 和 `relay` 已经内置了这些能力：

- bearer token 使用常量时间比较，避免直接字符串比较。
- `service` 的 Dashboard 不再把 owner token 原样写进 cookie。
- Dashboard 登录改成签名 session cookie，支持独立 `session_secret`。
- Dashboard 的写操作增加 same-origin 校验，降低跨站表单提交风险。
- 所有入口都支持请求体大小限制。
- 所有入口都支持基于来源 IP 的内存限流。
- 所有入口都支持 `allowed_hosts` 校验。
- 支持 `trusted_proxies`，便于在反向代理后正确识别真实客户端 IP。
- 默认加上安全响应头；如果前面已经有 HTTPS，可以开启 HSTS。
- HTTP server 增加 `ReadHeaderTimeout` 和 `IdleTimeout`，降低慢连接拖垮服务的概率。
- `relay` 可直接启用内置 HTTPS，并在缺少证书时生成自签证书。
- `relay` 暴露只读安装页 `/install` 和 APK 下载 `/install/apk`，方便首次从浏览器安装。
- Android 可对 relay 自签证书做一次性指纹信任；日常 API 优先使用 `device_token`，不继续携带 owner token。
- Host 文件服务只暴露配置或 owner 添加的 file roots；路径会清理并解析符号链接，必须留在对应 root 内。默认 roots 可下载，下载还要满足 `download=true` 和 `max_download_bytes`。

对应配置在：

- [service/config.example.json](../service/config.example.json)
- [relay/config.example.json](../relay/config.example.json)

## 公网边界防护

公网部署时，建议把 `service` 和 `relay` 放在反向代理后面，只暴露反向代理端口。

边界层建议负责：

- TLS 终止和证书续期
- 主机名收敛
- 较粗粒度的 IP 限流
- 请求体上限
- 统一日志和真实 IP 透传
- 只开放必要方法和端口
- 与主机级防火墙、fail2ban、云安全组联动

反向代理模板属于部署线，首轮公开主线只保留这份姿态说明。

## 个人部署的 HTTPS 路径

Watcher 不把安全模块做成复杂权限系统。推荐顺序是：

1. 优先用 Tailscale、局域网、反向代理或云安全组收敛入口。
2. 没有域名时，可在 `relay.security.tls` 开启内置自签 HTTPS。
3. Android 设置页填 `https://host:port` 后，先点 `Trust HTTPS Certificate` 固定证书指纹，再注册设备。
4. 证书更换后，在设置页重新信任即可。

自签 HTTPS 不要求使用 `443`。普通端口如 `8780` 也可以，只要 Android 填完整 URL。

`owner_token` 只作为首次设备注册和边界授权使用；设备注册成功后，Android 日常请求使用 relay 签发的 `device_token`。清缓存、重启等维护动作以二次确认为主，不要求用户反复输入随机 token。

## 推荐部署方式

1. `service` 只监听内网或本机地址。
2. `relay` 只监听内网或本机地址。
3. 反向代理监听 `:443`，再反代到内部端口。
4. 在 `trusted_proxies` 中只写你自己信任的反代网段。
5. 公网部署时，把 `secure_cookies` 设为 `true`，并开启 HSTS。
6. `allowed_hosts` 只保留实际使用到的域名。

## 最低上线清单

- owner token 和 session secret 都换成高熵随机值
- `service` 和 `relay` 不直接裸露在公网
- 反向代理启用 HTTPS
- `secure_cookies=true`
- `allowed_hosts` 配到正式域名
- `trusted_proxies` 配到真实反代网段
- 保留限流，不要为了“省事”直接关掉
