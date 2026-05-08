# Watcher Architecture

`watcher` 是一个单人使用的”工具终端”。

## 顶层结构

```
watcher/
├── service/        本机事实来源、规则中心、组件运行时
├── relay/          公网轻中继 — 事件转发、设备管理、推送分发
├── android/        小米设备客户端 — Shell UI、推送接收、后台同步
├── tools/          可选采集能力层 (首轮公开只保留协议说明)
├── modules/        组件定义和 manifest (opencode, box, host, archived refs)
├── internal/       Go 共享库 (push, store, model, workers, rules, ...)
├── pkg/            可复用包 (serverguard 安全中间件)
├── docs/           架构、模块、运维文档
└── devtools/       工程辅助工具 (scaffold 等)
```

## 数据流

### 任务事件流

```
service (规则中心)
  │
  ├─ rules engine → WatcherTaskEvent
  │
  ▼
relay (事件中继 + 推送)
  ├── 存储 event envelope (SQLite)
  ├── 自动 push 通知所有注册设备
  │
  │ MiPush HTTP API
  ▼
Android 客户端
  ├── MiPushReceiver → WorkManager 一次性同步
  ├── BackgroundSyncWorker → 拉取增量事件
  └── 本地通知展示
```

1. `service` 按任务计划调用某个 tool。
2. tool 输出 `SourceSnapshot`。
3. `service` 保存 snapshot，并与上一版做 diff。
4. `rules` 把变化转成 `WatcherTaskEvent`。
5. 事件进入本地 typed event store。
6. outbox 把事件投递到 `desktop`、`webhook`、`relay`。
7. `relay` 存储 envelope，自动通过 MiPush 通知客户端。
8. 客户端收到推送后触发后台同步，拉取增量事件。

### 组件运行流

```
Shell (service 主进程)
  ├── Opencode  light/in-process    主力 opencodev2 会话镜像
  ├── Host      light/in-process    服务器状态和文件下载
  ├── Box       light/in-process    信息源与数据视图
  ├── Codex     archived            legacy Codex 线程桥接
  ├── Pilot     archived            LLM 摘要/对话
  ├── CC MiMo   archived            Claude Code 会话
  └── Probe     archived            worker-lane 验证样板
```

## Shell Contract v2

组件通过 `component.json` manifest 注册，Shell 负责：

- 组件发现和注册表维护
- Worker 进程管理 (spawn / health / restart)
- Event Bus 分发
- Diagnostics 采集
- 移动端 UI 聚合 (`GET /api/v2/shell/home`)
- 模块能力、入口和动作发现 (`GET /api/v2/modules`)

模块呈现契约见 [Module Contract V2](foundation/MODULE_CONTRACT_V2.md)。Codex-v2 暂不实现；后续若重写，应复用 opencodev2 跑通后的模块契约和通用 conversation renderer。

## 安全

- 绑定主机白名单 (allowed_hosts)
- Body 大小限制
- 全局速率限制
- 同源检查
- Bearer Token owner auth
- Device Token 设备认证
- HSTS 可选

## 推送

当前实现：小米 MiPush (中国大陆)

推送模块详见 [PUSH_NOTIFICATIONS.md](/docs/modules/PUSH_NOTIFICATIONS.md)

预留：FCM (国际 Android) / APNs (iOS) / Huawei Push Kit / WebSocket SSE
