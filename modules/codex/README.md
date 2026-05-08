# Codex Module

> Status: archived reference
> Public mainline: no

`codex` 是 Watcher 的历史 Codex app-server/mobile bridge 组件。它现在归档为参考实现，不再作为开源主线模块、默认移动入口或新架构样板。

保留价值：

- app-server 协议研究、server request broker、operation/event 持久化
- 旧 Android thread 页面和 stale operation reducer 的经验
- 未来 Codex-v2 重写时的参考材料

新实现不应复制这一版的 Android/service 专用耦合。后续 Codex-v2 如需恢复，应复用 `opencodev2` 已落地的 service-owned conversation projection、module registry、operation lifecycle 和 typed pulse 模式。

## Historical Context

`codex` 是 `watcher` 的第二个组件。

## Identity

- `component`: `codex`
- `goal`: 把服务器上的 Codex 工作流自然带到手机上，同时保持桌面和手机可以连续工作
- `stage`: archived

## Responsibility

这个组件长期要解决四件事：

- 浏览共享线程池里的 Codex thread
- 在手机上继续一条已有 thread
- 在手机上发起和主管自己的 thread
- 处理移动端需要承接的 approvals、`requestUserInput`、review 等交互

它不负责：

- 重写 Codex runtime
- 定义 shell transport
- 定义设备同步协议
- 决定 app 更新分发机制

## Stable Decisions

这几条现在视为冻结：

- 共享同一个 `~/.codex`
- thread 仍然是 Codex 原生 thread
- `watcher-service` 自主管理 runtime
- `relay` 负责移动端事件分发
- 手机默认以同线程连续工作为主，不默认 fork

## Shell Dependencies

- 依赖的 shell 能力：
  - durable typed event bus
  - async operation 生命周期
  - owner-facing API 宿主骨架
  - android 壳层同步骨架
- 不绕过的 shell 约束：
  - 不以 follower IPC 作为正式产品主线
  - 不绕过 event bus 做移动同步
  - 不把 relay 做成 codex 业务判断层

## API Surface

- resources:
  - `thread`
  - `turn`
  - `operation`
  - `server_request`
- operations:
  - `thread/start`
  - `turn/start`
  - `turn/steer`
  - `review/start`
  - `turn/interrupt`

## Event Surface

- streams:
  - `codex.operation`
  - `codex.thread`
  - `codex.server_request`
- important kinds:
  - operation accepted / started / completed / failed
  - thread updated / busy / idle
  - server request created / resolved

## State Ownership

- long-term state:
  - operation、queue、pending request、overlay
- overlay state:
  - app-managed / desktop-attached / runtime-side metadata
- external source of truth:
  - 共享 `~/.codex` thread pool

## Runtime Ownership

- runtime owner:
  - `watcher-service`
- queue / broker / retry owner:
  - codex 组件在 `service` 内的 runtime / operation / broker 层

## Android Surface

- entry:
  - `Codex` 组件入口
- screens:
  - thread list
  - thread detail
  - operation / pending request 交互
- interaction style:
  - 紧凑、技术向、状态可解释

## Architecture

Codex 模块的正式链路是：

`android -> relay -> watcher-service -> codex app-server -> relay typed events -> android`

这里面：

- `service`
  负责 operation、queue、overlay、broker
- `relay`
  负责 typed event bus 和公网入口
- `android`
  负责线程列表、operation 视图、审批与追问交互

## Why Not Follower-First

`follower IPC` 仍然有价值，但它不是正式产品主线。

Codex 模块长期要沉淀的是：

- 对上游 `app-server` 协议的稳定对齐
- 对线程和 turn 的清晰读写模型
- 对移动端交互的可解释状态流

而不是去赌 VSCode 插件内部私有转发层一直稳定。

## Failure Model

- common failure classes:
  - runtime unavailable
  - operation failed
  - thread busy / queued
  - pending request unresolved
  - upstream protocol change
- user-facing expression:
  - 通过 typed operation / thread / server_request 事件表达，而不是靠文案猜状态

## Current Landing

当前已经落地的第一批基础设施是：

- relay `v2` typed event bus
- codex async operation persistence
- `POST /api/v2/modules/codex/threads/{threadID}/turns/start`
- `GET /api/v2/modules/codex/threads/{threadID}/operations`
- `GET /api/v2/modules/codex/operations/{operationID}`

这意味着 Codex 模块已经开始从“同步 prompt bridge”走向“异步 operation + event bus”。

## Public Docs

Only this short archive note is part of the public mainline. Detailed Codex
app-server research and historical implementation notes remain private unless
they are rewritten for a future Codex-v2 module.

## Non-Goals

- 不把 `codex` 做成“远程控制 VSCode”产品
- 不再把 rollout 扫描和私有 IPC 当正式主路径
