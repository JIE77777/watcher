# Watcher

[English](README.en.md)

Watcher 是一个自托管的个人工具终端。

它在你自己的机器上运行后端，只把 relay 暴露给外部网络，并提供 Android
客户端用于日常操作：服务器状态、文件下载、信息源、应用更新和 opencode
会话。

## 设计原则

Watcher 面向单人 owner 使用，不是多租户 SaaS。

```text
Android / browser / external network
        |
        v
watcher-relay      公网边界、设备认证、安装/更新、TLS、限流
        |
        v
watcher-service    本地事实来源、模块、opencode bridge、host 工具
```

`watcher-service` 通常只监听 `127.0.0.1`。外部只暴露 `watcher-relay`，
不要把 service 端口当作公网入口。

## 当前状态

Watcher 目前处于公开预览阶段。

公开主线刻意收敛，只推荐以下部分：

- `relay`：Android、安装/更新、设备认证和模块转发的外部边界
- `service`：本地运行时、SQLite 状态、模块 API 和诊断
- `android`：移动端壳层，承载 signal、模块、设置、更新和 opencode 会话
- `opencode`：主力 agent 模块，基于 opencode 原生 server/session API
- `host`：服务器状态和白名单根目录文件浏览/下载
- `box`：配置驱动的信息源组件；公开示例是 LLM 榜单 fixture

`codex`、`pilot` 和 `cc` 是归档参考模块。它们保留在仓库中作为历史上下文，
但不是推荐的新扩展路径。

## 主要能力

- 从自己的 relay 安装 Android APK
- 使用 relay owner token 注册 Android 设备
- 在 Android 上查看 shell 和组件诊断
- 查看服务器状态，并从配置好的根目录下载文件
- 用 Box 做热更新的信息源组件
- 在 Android 上查看、新建、读取和继续 opencode 原生会话
- 让 `service` 保持私有，只通过局域网、Tailscale 或公网 IP 暴露 `relay`

## 快速开始：预构建部署

如果你不想在服务器上安装 Go、Java、Gradle、Android SDK 或 Android 构建工具，
优先使用预构建发布包。

从这里下载发布资产：

```text
https://github.com/JIE77777/watcher/releases
```

解压预构建包：

```bash
mkdir -p ~/watcher
tar -xzf watcher-v0.3.1-linux-amd64-prebuilt.tar.gz -C ~/watcher --strip-components=1
cd ~/watcher
./deploy/prebuilt/install.sh
```

手动启动：

```bash
bin/watcher-service --config config/service.json
bin/watcher-relay --config config/relay.json
```

或者生成用户级 systemd 服务：

```bash
./deploy/prebuilt/install.sh --systemd --start
```

如果 Android 要从另一台设备访问，把 relay 绑定到可访问地址：

```bash
WATCHER_RELAY_BIND=0.0.0.0:8780 \
WATCHER_ALLOWED_HOSTS=127.0.0.1,localhost,<server-ip-or-domain> \
./deploy/prebuilt/install.sh --force --systemd --start
```

生成的本地密钥和配置位于：

```text
config/tokens.env
config/service.json
config/relay.json
```

请保持私有。Android 通常只需要 relay URL 和 relay owner token。

## 安装 Android 应用

relay 运行后，打开：

```text
https://<relay-host>:8780/install
```

如果没有启用 TLS，则使用 `http://`。

安装页本身可以打开，但 APK 下载受保护。输入一次 relay owner token 后，
relay 会创建短时安装会话。

公开预览 APK 适合个人部署。如果你自己重新构建 Android 应用，请保持同一个签名
key；Android 不允许用不同签名的 APK 覆盖更新已安装应用。

## 源码运行

依赖：

- 支持 CGO 的 Go 环境
- SQLite 构建依赖
- 只有自己构建 APK 时才需要 Android 工具链
- 只有使用 opencode 模块时才需要 `opencode`

从源码启动后端：

```bash
go run ./service/cmd/watcher-service --config ./service/config.example.json
go run ./relay/cmd/watcher-relay --config ./relay/config.example.json
```

健康检查：

```bash
curl http://127.0.0.1:8765/api/v1/health
curl http://127.0.0.1:8780/api/v1/health
```

本地 dashboard：

```text
http://127.0.0.1:8765/
```

dashboard 属于 `watcher-service`，定位是本机管理页。外部访问优先走 Android
或 relay 承载的页面，不建议直接转发 service 端口。

## 配置关系

预构建安装脚本会生成可用的本地配置。如果手动编辑，请保持这些关系：

- `service.owner_token` 保护本地 service dashboard 和 service API
- `relay.owner_token` 用于 Android 注册和 relay owner API
- `relay.service.owner_token` 必须等于 `service.owner_token`
- `service.relay.owner_token` 和 `service.relay_push.owner_token` 必须等于 `relay.owner_token`
- `service.bind_addr` 通常保持 `127.0.0.1:8765`
- `relay.bind_addr` 是 Android 应该访问的地址
- `service.security.allowed_hosts` 应填写实际使用的 host

opencode server 模式下，Watcher 默认遵循 opencode 的 Basic Auth 用户名：

```json
{
  "opencode": {
    "driver": "server_adapter",
    "server_username": "opencode",
    "server_password": "your-random-password"
  }
}
```

如果你的 opencode 环境覆盖了 `OPENCODE_SERVER_USERNAME`，请把
`server_username` 设置为相同值。

## 模块

### Opencode

opencode 模块是主力 agent 表面。Watcher 会启动或连接 opencode server，把
原生 `ses_*` 会话镜像到本地 SQLite，并向 Android 暴露适合移动端的
`snapshot` 和 `pulse` API。

当前 Android 应优先使用 `opencode-mirror`。旧的 Watcher turn/session API
保留为兼容和历史参考。

### Host

Host 是轻量服务器工具模块：

- CPU、内存、磁盘概览
- 配置好的文件根目录
- 面包屑式目录浏览
- 带根目录和大小限制的文件下载

它只面向读取和下载。上传、删除、重命名、移动和远程 shell 都不是目标。

### Box

Box 是非 agent 模块的公开示例。一个 `.box.json` 文件描述 source、dataset、
view 和 signal。Android 在运行时获取最新 catalog 和 view schema，因此修改
Box 定义不需要重新构建应用。

公开仓库包含一个安全的 LLM 榜单 fixture。私有 scraper 驱动的 Box 应保持在
公开导出之外。

## 安全模型

Watcher 的安全层保持轻量但边界明确：

- 唯一外部边界：`watcher-relay`
- owner token 用于首次信任和 owner API
- device token 用于已注册 Android 客户端
- host 白名单
- 请求体大小限制
- 按 IP 限流
- 安全响应头
- 可选 relay 自签 HTTPS
- service dashboard 同源检查

如果要公网暴露 relay 且没有域名，可以启用内置自签 HTTPS，并在 Android
设置页信任一次证书指纹。relay 端口不需要是 `443`。

## 目录结构

```text
android/        Android 客户端
service/        本地运行时和模块 API
relay/          外部中继、安装更新、设备认证
internal/       Go 共享包和 SQLite 存储
pkg/            可复用公开包
modules/        组件 manifest 和模块文档
tools/          公开工具和 parser 占位
deploy/         预构建部署脚本
devtools/       导出、发布、烟测和脚手架工具
docs/           架构、安全、模块和部署文档
```

## 开发检查

后端：

```bash
go test ./pkg/serverguard ./service/cmd/watcher-service ./relay/cmd/watcher-relay
```

Android debug 构建：

```bash
cd android
./gradlew :app:assembleDebug --no-daemon
```

公开导出审计：

```bash
devtools/public/audit_public.sh --target ../watcher-public
```

## 文档

- [Prebuilt Deployment / 预构建部署](docs/PREBUILT_DEPLOYMENT.md)
- [Environment Setup / 环境配置](docs/ENVIRONMENT_SETUP.md)
- [Architecture / 架构](docs/ARCHITECTURE.md)
- [Security / 安全](docs/SECURITY.md)
- [Open Source Readiness / 开源准备](docs/foundation/OPEN_SOURCE_READINESS.md)
- [Public Export / 公开导出](docs/foundation/PUBLIC_EXPORT.md)
- [Module Contract V2 / 模块契约](docs/foundation/MODULE_CONTRACT_V2.md)
- [Opencode Agent / Opencode 模块](docs/modules/OPENCODE_AGENT.md)
- [Host Module / Host 模块](docs/modules/HOST_MODULE.md)
- [Android Connection Troubleshooting / Android 连接排查](docs/ANDROID_CONNECTION_TROUBLESHOOTING.md)
- [Contributing / 贡献指南](CONTRIBUTING.md)
- [License / 许可证](LICENSE)

## 非目标

- 多用户 SaaS 托管
- 把 `watcher-service` 暴露成公网 API 网关
- 通用 agent 市场
- 从 Android 任意执行远程 shell
- 公开导出私人 scraper 或个人自动化工具
