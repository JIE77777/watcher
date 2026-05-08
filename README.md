# Watcher

`watcher` 现在已经从单用途脚本扩成一个“个人工具终端”骨架：

- `tools/`：采集层
- `devtools/`：工程辅助工具层
- `service/`：本机任务调度、快照、diff、typed event、投递
- `relay/`：typed durable event bus 与设备入口
- `android/`：移动壳层、模块入口、设置/诊断与会话客户端
- `docs/`：协议和架构说明

项目按 `public_preview` 线准备开源，面向有同类需求的 self-hosted
owner：自己运行个人服务器、relay、Android 终端和本地 coding-agent
工作流。开源要求见 [Open Source Readiness](docs/foundation/OPEN_SOURCE_READINESS.md)。

`box` 作为配置驱动的信息源组件进入公开范围，内置 public-safe 的 LLM
leaderboard 示例；现有个人抓取工具只作为 private box source 保留，不进入公开导出。

## Public Mainline

首轮公开主线只推荐这几部分：

- base shell：`service`、`relay`、Android、typed event、operation、module contract
- `opencode`：opencode native session 的 Android conversation bridge
- `box`：配置驱动的信息源 / dataset / view 示例
- `host`：服务器状态监控和安全文件下载工具

`codex`、`pilot`、`cc` 保留为 archived reference，不作为新用户入口或扩展示范。

## 当前可用能力

- 本地 Dashboard：
  - 浏览器打开 `http://127.0.0.1:8765/`
  - 支持 owner token 登录、任务创建、启停、手动运行、最近 watcher.task feed 查看
  - 登录使用签名 session cookie，不再把 owner token 原样放进浏览器 cookie
- 本机 `service` 支持：
  - `GET /api/v1/health`
  - `GET /api/v1/tools`
  - `GET /api/v1/tasks`
  - `POST /api/v1/tasks`
  - `POST /api/v1/tasks/{id}/run`
  - `GET /api/v2/shell`
  - `GET /api/v2/shell/home`
  - `GET /api/v2/shell/diagnostics`
  - `GET /api/v2/components`
  - `GET /api/v2/components/{component_id}`
  - `POST /api/v2/components/{component_id}/restart`
  - `GET /api/v2/modules`
  - `GET /api/v2/modules/{component_id}`
  - `GET /api/v2/modules/host/overview`
  - `GET /api/v2/modules/host/files`
  - `GET /api/v2/modules/host/files/download`
  - `GET /api/v2/modules/opencode-mirror/sessions`
  - `GET /api/v2/modules/opencode-mirror/sessions/{native_session_id}/snapshot`
  - `GET /api/v2/modules/opencode-mirror/sessions/{native_session_id}/pulse`
  - `POST /api/v2/modules/opencode-mirror/sessions/{native_session_id}/messages`
- `codex`、`pilot`、`cc` 已归档为 reference modules，不属于开源主线、默认 Android 工具入口或新架构样板。
- `box` 使用 `.box.json` 热更新 catalog / dataset / view；公开版只包含 LLM 示例，私有抓取源留在 private 配置。
- `relay` 支持：
  - `POST /api/v1/devices/register`
  - `POST /api/v2/events/publish`
  - `GET /api/v2/events/since`
  - `POST /api/v2/events/{event_id}/ack`
  - `GET /api/v2/shell`
  - `GET /api/v2/shell/home`
  - `GET /api/v2/shell/diagnostics`
  - `GET /api/v2/components`
  - `GET /api/v2/components/{component_id}`
  - `POST /api/v2/components/{component_id}/restart`
  - `GET /api/v2/modules`
  - `GET /api/v2/modules/{component_id}`
  - `GET /api/v2/modules/host/overview`
  - `GET /api/v2/modules/host/files`
  - `GET /api/v2/modules/host/files/download`
  - `GET /api/v2/modules/opencode-mirror/sessions`
  - `GET /api/v2/modules/opencode-mirror/sessions/{native_session_id}/snapshot`
  - `GET /api/v2/modules/opencode-mirror/sessions/{native_session_id}/pulse`
  - `POST /api/v2/modules/opencode-mirror/sessions/{native_session_id}/messages`
- 内置安全层：
  - host allowlist
  - body size limit
  - per-IP rate limit
  - trusted proxy real IP 识别
  - 安全响应头
  - Dashboard same-origin POST 防护
- `android` 现在支持：
  - `Signals / Tools` 双页壳：首页只显示组件筛过的短 signal，Tools 是图标式组件入口
  - `System` 设置页收纳 relay、更新、设备、Shell/component diagnostics、worker restart、crash/debug report、cache reset
  - 中英显示切换与时区显示配置，默认 `Asia/Shanghai`；后端事实时间仍保持 UTC
  - 设置页保存 relay URL / owner token
  - 设置页查看 shell / component / worker 运行状态与最近诊断
  - 设置页复制 Android developer diagnostics，包含最近崩溃栈、设备/App/内存概况
  - 在设置页对 worker 组件发起最小恢复动作 `restart`
  - 测试 relay 连通性
  - `Update App` 按钮，通过 relay 检查并下载最新 APK
  - 注册设备
  - 缓存最近 watcher.task feed
  - 后续新增 watcher.task 事件的本地通知
  - `Opencode` session 列表和 session detail，优先使用服务端 conversation projection
  - 通过 typed event + async operation 驱动手机侧交互

## Quick Start

If you want to deploy released binaries and an APK without compiling on the
server, use [Prebuilt Deployment](docs/PREBUILT_DEPLOYMENT.md).
The prebuilt path contains no private config; run
`./deploy/prebuilt/install.sh` after extracting the release package to generate
local tokens and `config/*.json`.

1. 把 Go 加进当前 shell：

```bash
export PATH=/path/to/go/bin:$PATH
```

2. 启动 relay：

```bash
cd <watcher-workspace>
go run ./relay/cmd/watcher-relay --config ./relay/config.example.json
```

如果你想让手机端访问模块 API，relay 配置里的 `service.base_url` 和 `service.owner_token` 也要指向本机 `watcher-service`。

relay 也提供首次安装入口：

```text
http://127.0.0.1:8780/install
```

APK 下载需要用 relay owner token 解锁一个短时安装会话；应用内更新继续走设备 token/owner token 鉴权。

没有域名时，可以在 relay 配置的 `security.tls` 开启内置自签 HTTPS。Android 设置页支持一次性信任 relay 证书指纹，端口不必使用 `443`。

3. 启动 service：

```bash
cd <watcher-workspace>
go run ./service/cmd/watcher-service --config ./service/config.example.json
```

4. 打开 Dashboard：

```text
http://127.0.0.1:8765/
```

默认 owner token 在 [service/config.example.json](service/config.example.json) 里是 `change-me-owner-token`。

## Docs

- [Docs Index](docs/README.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Open Source Readiness](docs/foundation/OPEN_SOURCE_READINESS.md)
- [Public Export](docs/foundation/PUBLIC_EXPORT.md)
- [Opencode Agent](docs/modules/OPENCODE_AGENT.md)
- [Host Module](docs/modules/HOST_MODULE.md)
- [Android Connection Troubleshooting](docs/ANDROID_CONNECTION_TROUBLESHOOTING.md)
- [Environment Setup](docs/ENVIRONMENT_SETUP.md)
- [Prebuilt Deployment](docs/PREBUILT_DEPLOYMENT.md)
- [Security](docs/SECURITY.md)
- [Tool Protocol](docs/TOOL_PROTOCOL.md)
- [Versioning](docs/VERSIONING.md)
- [Watcher Modules](modules/README.md)
- [Contributing](CONTRIBUTING.md)
- [License](LICENSE)
