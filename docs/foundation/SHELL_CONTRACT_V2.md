# Shell Contract V2

`watcher shell` 的正式宿主契约固定为 `v2`。

## 核心约束

- 跨设备事件只允许通过 typed `EventEnvelope`
- 移动端写操作只允许通过 async operation
- 组件不得自定义 transport、auth、sync
- relay 只承载 durable typed event bus，不承接组件业务判断
- light component 运行在 `watcher-service`
- heavy component 运行在 shell-managed worker

## 稳定对象

- `ShellManifest`
- `ComponentManifest`
- `ComponentStatus`
- `ModuleDescriptor`
- `ModuleSurface`
- `ModuleAction`
- `ShellDiagnosticEvent`
- `ComponentOperation`
- `EventEnvelope`
- `WatcherTaskEvent`

## 壳层运维面

- `GET /api/v2/shell`
- `GET /api/v2/shell/diagnostics`
- `GET /api/v2/components`
- `GET /api/v2/components/{component_id}`
- `POST /api/v2/components/{component_id}/restart`
- `GET /api/v2/modules`
- `GET /api/v2/modules/{component_id}`

其中：

- `ShellStatus` 负责壳层版本、contract、relay/event bus 摘要和 component counts
- `ComponentStatus` 负责 manifest 校验、runtime 状态、worker 诊断字段
- `ModuleDescriptor` 负责给客户端暴露模块能力、入口、默认目标和 owner actions
- `ShellDiagnosticEvent` 负责最近错误、worker crash/backoff/restart 和 event publish 失败记录

## 组件清单要求

每个 `modules/<component>/component.json` 必须声明：

- identity
- release line / channel
- shell contract
- component class
- runtime shape
- runtime owner
- capabilities
- streams / resources / operations
- surfaces / default target / actions
- android surfaces
- shell dependencies
- docs / non-goals

`runtime_shape=worker` 时还必须声明 `worker` block：

- `entrypoint`
- `args`
- `env`
- `healthcheck`
- `operations`
- `streams`

## 模块呈现契约

Shell Contract v2 不再要求 Android 直接理解所有组件内部模型。

组件通过 manifest 声明：

- `capabilities`: 能力组合，例如 `feed`、`interactive_session`、`pending_input`
- `surfaces`: 可进入页面，每个页面都有稳定 `ShellTarget`
- `default_target`: 默认入口
- `actions`: owner 可以触发的动作及其 async / destructive / confirmation 属性

正式说明见 [Module Contract V2](MODULE_CONTRACT_V2.md)。

## 统一操作生命周期

- `accepted`
- `queued`
- `running`
- `waiting_user_input`
- `completed`
- `failed`
- `interrupted`

## Worker 协议

shell -> worker:

- `spawn.init`
- `health.ping`
- `operation.start`
- `operation.cancel`
- `shutdown`

worker -> shell:

- `health.ok`
- `operation.update`
- `event.publish`
- `log.line`
- `shutdown.ready`
