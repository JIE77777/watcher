# Shell And Component Task Split

这份文档把 `watcher` 后续任务按两条主线拆开：

1. `watcher shell`
2. `watcher components`

这样做的目的，是让壳子以后可以单独发布，同时又不把组件开发卡死在宿主层改动上。

## Decision

从现在开始：

- `shell` 是正式的宿主主线
- `opencodev2` 是当前公开参考组件主线
- `box` 是当前公开非 Agent 组件主线；private source 不进入公开导出
- `codex`、`pilot`、`cc` 是归档参考组件
- 开发仍然先保留在一个仓库里
- 但任务管理、文档、边界、验收按 shell / component 分开

## Shell Workstream

下面这些任务默认属于 `watcher shell`：

### Contract

- 稳定的 API 约定
- 稳定的 event envelope
- 稳定的 async operation 生命周期
- 跨组件通用错误表达

### Infra

- `service` 宿主骨架
- `relay` durable event bus
- `android` 壳层导航和同步骨架
- 设备注册与 owner 入口

### Delivery

- app 更新链路
- 壳层版本策略
- 组件接入和开关策略

### Observability

- 健康检查
- 日志
- ledger
- 诊断入口

### Governance

- 组件规范
- 组件模板
- 新组件接入检查清单

## Component Workstream

下面这些任务默认属于组件：

### `opencode`

- native opencode session mirror
- snapshot / pulse / conversation projection
- async message operation
- pending input and abort handling
- Android conversation surface

### Archived components

- 共享 thread pool 交互
- protocol-native thread / turn 读写
- operation、queue、broker
- approvals、`requestUserInput`、review
- historical Codex / Pilot / CC lessons only; not new public extension points

### Future Components

未来组件也按同样结构进入系统：

- 先定义组件边界
- 再接入 shell 契约
- 最后再收实现

## Ownership Heuristic

遇到归属不清时，用这条判断：

- 被两个以上组件共享：归 `shell`
- 只服务一个组件：归该组件
- 只是一层 transport / sync / identity：归 `shell`
- 是领域语义、状态机或交互：归组件

## Execution Order

建议按这个顺序推进：

1. 先稳住 shell 契约
2. 让 `opencodev2` 完整跑在 shell 上
3. 让 `box` 的 public LLM leaderboard 示例和 private source adapter 都跑在同一套 catalog/view schema 上
4. 再考虑壳子独立发布节奏
5. 最后再考虑物理拆仓库

## Done Criteria

### Shell Ready

满足下面这些条件，才算 shell 真的立住：

- 新组件不需要发明新的同步协议
- 新组件不需要自带鉴权和设备接入
- 新组件可以直接复用 async operation + event bus
- app 壳层不需要为每个组件复制一套基础交互骨架

### Component Ready

满足下面这些条件，才算组件真的成熟：

- 边界清晰
- 读写面明确
- 事件流明确
- 失败模型明确
- app 入口和状态表达明确

## Current Immediate Split

当前最适合直接推进的任务拆分是：

### Shell Immediate

- 收紧 shell 文档和治理
- 稳定 event bus 与 operation 契约
- 稳定 android 壳层同步骨架
- 补通用诊断和观测

### Opencode Immediate

- 继续压实 mirror session / conversation projection 主线
- 把移动端交互完整建立在 operation + pulse 上
- 清理剩余 v1 / legacy managed-turn 思维

### Box Immediate

- public fixture 使用 LLM leaderboard
- private 工具通过 `modules/box/private/*.box.json` 接入
- Android 只消费 catalog / dataset / view，不写死具体信息源

## What Not To Do

当前阶段不建议做：

- 为了“独立发布”立刻拆多仓库
- 为了“通用”把组件逻辑过早抽进 shell
- 为了“快速接一个组件”给它开私有 bypass

这些都会让后面的独立开发和独立发布更难，而不是更容易。
