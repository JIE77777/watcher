# Shell And Components

这份文档把 `watcher` 之后的产品组织方式收敛成一个明确模型：

`watcher = shell + components`

目标不是把概念说漂亮，而是避免未来因为边界不清而把开发卡死。

## Why This Split

这个拆分是为了同时满足三件事：

- `watcher` 壳子以后可以单独发布
- 新能力可以作为组件独立开发
- 组件之间不需要互相知道太多内部实现

如果不先把这层写死，后面很容易出现两种坏结果：

- 壳子越来越像一个装满业务逻辑的混合物
- 组件表面独立，实际上每个都偷连底层实现，最后谁都没法单独演进

## What Is The Shell

`shell` 是宿主层，不是某个组件的总和。

它长期负责：

- 设备身份与 owner 入口
- `service` / `relay` / `android` 三层宿主骨架
- durable typed event bus
- async operation 生命周期
- 统一的 API 约定
- app 发布、更新与版本分发
- 跨组件共用的存储、日志、健康检查、观测

`shell` 的职责是“让组件可以被承载”，而不是“替组件做业务决策”。

## What Is A Component

`component` 是挂在 shell 上的一块产品能力。

它长期负责：

- 自己的领域模型
- 自己的运行时语义
- owner-facing 资源和操作
- 自己的事件 vocabulary
- 自己在 app 里的入口和交互面

当前公开主线组件：

- `opencode`
- `box`
- `host`

私有或归档组件：

- `box/private` sources：现有个人实验工具链，只作为私有 source adapter 数据来源
- `codex`、`pilot`、`cc`、`probe`：归档参考，不作为新组件样板

后面新增能力时，优先先定义成一个组件，而不是直接把代码散落到 `service`、`relay`、`android`。

## Boundary

### Shell Owns

- transport
- device registration
- owner auth
- event bus
- async operation 协议
- shared storage primitives
- app release
- health and observability

### Component Owns

- 领域 API
- 领域事件
- runtime orchestration
- 本组件的数据模型
- 本组件的失败语义
- 本组件的 UI 入口

### Shell Must Not Do

- 不把某个组件的业务语义硬编码进基础设施
- 不给某个组件开私有 bypass
- 不把 relay 做成业务判断层
- 不把 android 做成服务器 runtime 的替身

### Component Must Not Do

- 不自建 transport
- 不绕过 event bus
- 不自带一套设备身份与同步协议
- 不直接依赖另一个组件的内部实现
- 不把自己的状态机偷偷塞进 shell 通用层

## Dependency Direction

允许的依赖方向：

- `component -> shell contracts`

明确禁止的依赖方向：

- `shell -> component business logic`
- `component A -> component B internals`
- `android screen -> service internal structs`
- `component -> relay raw storage layout`

如果某段能力会被两个以上组件共享，它就应该回到 shell。

如果某段能力只服务一个组件，它就应该留在组件里，不要过早抽成“通用基础设施”。

## Release Model

目标是：

- `shell` 可单独发布
- `component` 可单独开发

但当前阶段不急着拆成多仓库。

推荐顺序是：

1. 先把 shell 契约稳定下来
2. 再把组件读写面、事件面、状态面收成清晰边界
3. 再做独立版本和独立发布
4. 最后才考虑物理拆仓库

这样做能避免过早拆仓库带来的两类瓶颈：

- 共享类型和工具还没稳定，就开始跨仓协调
- 组件看似独立，实际每次改动都要双边同时改

## Bottlenecks To Avoid

最容易把系统做死的是下面这五类设计：

### 1. Fake Modularity

表面有组件，实际上每个组件都依赖 shell 的私有实现细节。

结果：

- 组件无法独立开发
- shell 无法独立发布

### 2. Shell Bloat

把组件逻辑不断抽进 shell，最后壳子成了最大组件。

结果：

- 新组件越来越难接
- 基建改动风险越来越大

### 3. Component Bypass

组件直接操作 relay、数据库、设备同步、更新通道。

结果：

- 系统很快长出多套传输和状态模型
- 安卓端会被不同组件拖成几种交互范式

### 4. Shared Private Types

组件和 shell 共用一堆“顺手拿来”的内部结构体。

结果：

- 任何一侧小改动都会带来连锁重构
- 版本边界永远立不住

### 5. UI Coupling

每个组件都自己发明一套移动端数据流和状态表达。

结果：

- app 无法保持克制一致
- 后续再加组件时需要复制一套又一套交互骨架

## Working Rule

后续做需求时，先问三个问题：

1. 这是 shell 问题，还是 component 问题？
2. 这个能力会不会被至少两个组件共享？
3. 它应该沉淀成稳定契约，还是保留为某个组件内部实现？

如果这三个问题答不清，先不要写代码。
