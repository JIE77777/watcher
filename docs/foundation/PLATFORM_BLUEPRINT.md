# Platform Blueprint

`watcher` 的目标不是一个单功能 app，而是一个长期可扩展、克制、偏极客的个人服务器终端。

现在把它的产品形态正式收成一句话：

`watcher = shell + components`

这里的：

- `shell`
  指 `service + relay + android` 以及它们共享的稳定契约。
- `components`
  指跑在 `watcher` shell 之上的产品能力，比如 `opencode`、`box` 或后续组件。

当前公开主线是：

1. `opencodev2`
   负责把本地 opencode 会话镜像到 Watcher shell 和 Android。
2. `box`
   负责把用户定义的信息源变成可热更新的 dataset / view / signal。

现有个人抓取工具只作为 private box source 保留，不作为公开默认数据源。

## Product Shape

`shell` 负责这些长期稳定的底座：

- 设备身份与 owner 入口
- durable typed event bus
- async operation 生命周期
- 公网 relay 和移动同步
- app 更新分发
- 跨组件共享的存储、观测、健康检查

`component` 负责这些领域能力：

- 领域模型
- 运行时语义
- owner-facing API
- 组件自己的 typed event vocabulary
- 在 app 中的入口与交互面

这层现在已经有正式元数据落点：

- [watcher.shell.json](../../watcher.shell.json)
- `modules/<component>/component.json`

## Platform Rules

后续实现统一遵循这几条规则：

- 新能力先判断属于 `shell` 还是某个 `component`，再决定代码落点。
- `shell` 持有跨组件稳定语义；`component` 持有领域语义。
- `service` 持有业务运行时、执行状态和长期状态。
- `relay` 只做公网接入、设备同步、事件分发，不承接组件业务判断。
- `android` 只做客户端体验和交互编排，不直接拥有服务器 runtime。
- 组件之间通过 typed API 和 typed event 连接，不靠弱语义文案消息粘起来。
- 新组件不得绕过 shell 自定义 transport、鉴权、同步或更新机制。
- shell 不得内置某个组件专属语义，否则会把壳子做成业务垃圾桶。

## Infra Roles

### `service`

- 持有模块运行时
- 维护本地状态库
- 提供 owner-facing 和 relay-facing API
- 对外产出 typed event

### `relay`

- 设备注册
- cursor 同步
- durable event bus
- app release 分发
- 作为公网入口转发到 `service`

### `android`

- 设备身份
- 模块入口
- 查询与事件消费
- 极客、克制、信息密度高的移动 UI

这三层共同构成 `watcher shell`，不是某个组件自己的实现细节。

## Event Model

旧的 `MessageEvent` 只适合 inbox 类提醒，不适合作为模块主事件模型。

从 Codex 这轮开始，`watcher` 的正式实时通道改成 typed envelope：

- `stream`
- `kind`
- `thread_id`
- `turn_id`
- `operation_id`
- `request_id`
- `payload`
- `occurred_at`

推荐流划分：

- `opencode.session`
- `opencode.turn`
- `opencode.permission`
- `watcher.task`
- `system.release`

组件只能在这些 envelope 规则之上扩展自己的 `kind` 和 `payload`，不能各自发明一套新的移动同步协议。

## API Rules

新的模块 API 默认遵循这几条：

- 新设计优先走 `v2`
- 优先使用 typed resource，而不是杂糅型 payload
- 写路径优先走 async operation
- 结果和进度优先走 event bus，而不是同步阻塞等待整轮完成

当前壳层状态入口：

- `GET /api/v2/shell`
- `GET /api/v2/components`

## Release Rule

从现在开始，发布和开发按下面的节奏理解：

- `shell` 是一个可单独发布的宿主层
- `component` 可以在同一仓库内独立演进
- 独立发布优先先做“契约独立”和“版本独立”，不急着先拆成多仓库
- 只有当 shell 契约稳定、组件边界清楚后，再考虑物理拆仓库

## Current Landing Slice

这轮已经落地的基础设施切片是：

- relay `v2` typed event bus
- shell manifest + component registry read path
- `watcher.task` 通过 typed envelope 下发到移动端
- opencode mirror snapshot / pulse / conversation projection
- opencode async message operations and abort path
- android 统一改走 `v2` event sync

对应的最小链路现在是：

`android -> relay -> service -> module runtime -> relay typed events -> android`

## What Stays Stable

下面这些边界默认视为稳定，不再轻易反复：

- `watcher` 的正式产品形态是 `shell + components`
- `relay` 是长期保留的公网总线层
- 组件业务语义不落在 `relay`
- 新能力优先通过 typed event 和 async operation 进入系统
- 独立开发优先靠稳定契约，不靠私有实现耦合
- `box` 框架公开；private source 不进入公开导出
