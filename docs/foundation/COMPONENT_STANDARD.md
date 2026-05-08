# Component Standard

这份文档定义 `watcher component` 的最小规范。

目标是让“可以单独开发组件和壳”这件事不是口号，而是能落地的工程约束。

## Required Files

每个组件至少要有：

- `modules/<component>/README.md`
- `modules/<component>/component.json`
- 至少一份放在 `docs/modules/` 下的运行时或架构文档

如果组件已经进入实现期，还应该补上：

- 最小 smoke 验证说明
- 事件流说明
- 写路径说明

## Required Sections

`modules/<component>/README.md` 至少要回答下面这些问题：

### 1. Identity

- 组件名
- 一句话目标
- 当前阶段

### 2. Responsibility

- 这个组件负责什么
- 不负责什么

### 3. Shell Dependencies

- 依赖哪些 shell 能力
- 明确不绕过哪些 shell 约束

### 4. API Surface

- 对外资源
- 对外操作
- 写路径是否 async

### 5. Event Surface

- 会发哪些 stream
- 每个 stream 的主要 `kind`
- app 依赖哪些事件推进状态

### 6. State Ownership

- 本组件持有哪些长期状态
- 哪些状态只是 overlay
- 哪些事实以外部系统为准

### 7. Runtime Ownership

- 谁持有 runtime
- 谁调度
- 谁负责 broker / queue / retry

### 8. Android Surface

- app 中入口在哪
- 核心屏幕是什么
- 交互风格约束是什么

### 9. Failure Model

- 常见失败类型
- app 应该如何表达这些失败

### 10. Non-Goals

- 当前明确不做什么

## Naming Rules

- 组件名使用短小的小写 ASCII
- stream 命名统一用 `<component>.<resource>`
- 新组件默认先走 `v2`

## API Rules

组件 API 统一遵循：

- manifest 先声明 capability / surface / action，再暴露具体 endpoint
- 读路径优先 typed resource
- 写路径优先 async operation
- 进度与结果优先 event bus
- 不把最终产品语义塞进自由文本字段

## Module Contract Rules

`component.json` 里的 module contract 字段面向 Shell、Android、Relay 和后续第三方实现者：

- `capabilities` 描述能力组合，不描述固定模块类型
- `surfaces` 描述可呈现入口，并给出稳定 `ShellTarget`
- `default_target` 是客户端不知道该进哪个页面时的安全入口
- `actions` 描述 owner 可触发动作，危险动作必须标明 `destructive` 或 `requires_confirmation`

Android 可以理解 `surface.kind` 和 `ShellTarget`，但不应该依赖 service 内部结构体或某个 runtime 的私有字段。

## Event Rules

组件事件统一遵循：

- 使用 `EventEnvelope`
- `stream` 和 `kind` 必须稳定、可枚举
- `payload` 可以演进，但不能随手漂移语义
- app 侧应依赖 typed 字段，不依赖文案解析

## Ownership Rules

下面这些属于 shell，不属于组件：

- 设备注册
- owner token / auth
- relay 同步语义
- app 发布和更新
- 通用 event bus

下面这些默认属于组件：

- 领域状态机
- 组件 API
- 组件 event vocabulary
- 组件特有存储和 overlay

## Testing Gate

组件进入“可用”状态前，至少应有：

- 资源读路径测试
- 写路径状态流测试
- 事件流 smoke
- app 基本交互验证

## Decision Template

新增组件或大改组件前，先把下面这几行填出来：

- `product tone fit`
- `component`
- `goal`
- `shell dependencies`
- `resources`
- `operations`
- `streams`
- `capabilities`
- `surfaces`
- `actions`
- `long-term state`
- `runtime owner`
- `android surfaces`
- `non-goals`

其中 `product tone fit` 必须说明它如何保持 `watcher` 的个人服务器终端气质，Android 入口属于 `Signals`、`Tools` 还是 `System`，以及是否存在 full-access / destructive 动作。

填不清楚时，说明边界还没成熟。

## Scaffold Rule

新增组件优先使用 `go run ./devtools/cmd/component-scaffold` 起步。脚手架必须生成当前 module contract 字段：

- `capabilities`
- `surfaces`
- `default_target`
- `actions`

如果手写 manifest，也必须补齐这些字段再进入实现期。
