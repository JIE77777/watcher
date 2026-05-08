# Product Tone

> 版本: 0.3  
> 作用：产品格调的硬边界。任何新增组件、重大改造、Android 入口调整，设计前必须过此页。不过此页就写代码，等同于绕过 owner auth。

---

## 一句话

**Watcher 是一个单人 owner 的、事件与操作驱动的、可离线运行的个人服务器终端。**

三个定语缺一不可：
- **单人 owner** = 不解释、不引导、不为陌生人设计
- **事件与操作驱动** = 状态机是 typed，不是自然语言
- **可离线运行** = 不假设云端在线，relay 是可选通道不是必要依赖

它不是：多人 SaaS、聊天机器人、AI Agent 菜单、移动端后台、营销式 App、任何需要" onboarding 流程"的东西。

## 开源取向

Watcher 准备开源，但开源不改变产品主语。

目标用户是有相同需求的 self-hosted owner：

- 自己有一台常开的个人服务器或开发机
- 希望把本地工具、AI coding agent、Android 终端和公网 relay 收成一个可诊断系统
- 能阅读配置、日志和文档，不需要 SaaS 式引导

开源要求是：

- 新机器能按文档复现最小可用链路
- 配置示例不能绑定个人路径、真实 token、私有域名或不可公开环境
- 安全边界、默认监听地址、owner token、relay 暴露方式必须写清楚
- 模块边界和非目标必须比功能卖点更清楚
- `opencode` 对外讲 v2 mirror 主线；第一版 Watcher-managed turn 路线归档为参考，不作为公开主故事

开源不是：

- 改成多人产品
- 增加欢迎页、新手教程、社区页或推荐流
- 默认接入云服务
- 为了“易用”绕过 owner auth、typed event、async operation 或 component manifest

---

## 稳定形态

```
watcher = shell + components
```

**shell** 是骨架：
- transport、owner auth、event bus、async operation
- 设备同步、发布、诊断、保活
- 不碰领域业务，只做契约和调度

**component** 是器官：
- 领域资源、领域操作、领域事件、运行时语义
- 可以有自己的页面、状态机、工具链
- 不能改 shell 的骨架结构

**Android** 是终端：
- 看信号（Signals）
- 进组件（Tools）
- 处理必要交互和恢复（System）
- 不是控制台，不是 IDE，不是聊天窗口

**relay** 是邮差：
- 公网入口 + durable event bus
- 做投递，不做业务判断
- 离线时 App 仍能看缓存、玩游戏、查历史

---

## 调性规则

### 1. Owner-first

**正例**：
- "配置 relay 后开始同步" — 假设用户知道 relay 是什么
- "worker crash，查看 diagnostics" — 直接给诊断入口

**反例**：
- "欢迎使用 Watcher！让我来引导您完成设置..." — 不需要欢迎，不需要引导
- "什么是 relay？" — 不需要解释，owner 应该已经读过文档

### 2. Quiet utility

**正例**：
- "3 jobs · 1 running · 2 idle" — 数字即状态
- "● 已连接 · <server-host>" — 事实即 UI

**反例**：
- "太棒了！您已成功连接到 relay！" — 过度反馈
- "这里可以查看您的所有任务哦~" — 不需要"哦"

**原则**：短标题、短状态、明确动作、可诊断事实。一个界面上超过 20% 的空间用于解释性文案，就是失败。

### 3. Event and operation driven

**正例**：
- `"type": "event", "stream": "opencode.turn", "kind": "turn.running"` — 事件驱动刷新
- `"status": "running", "resource_id": "turn_abc123"` — 状态机明确

**反例**：
- "看起来有些任务完成了，让我来帮您刷新一下..." — 自然语言不是状态机
- 用 LLM 输出解析决定按钮是否可用 — 不可预测

### 4. Mobile as terminal

首页只有三页：
- **Signals**：组件筛过的短状态。一行一个，不超过 5 条。
- **Tools**：组件入口。图标+短标签，不超过 8 个。
- **System**：设置、诊断、更新、恢复。必要但不喧宾夺主。

**红线**：首页不能变成对话窗口、代码编辑器、数据仪表盘或任务列表。

### 5. Components stay components

AI、抓取、诊断、解释、代码执行都只是组件能力。

**红线**：任何组件不能反过来改写 shell 的产品主语。比如：
- codex 不能要求在首页常驻一个 "New Thread" 按钮
- opencode 不能把 ShellHome 的 Signals 挤到第二页
- example component 不能让 Android 变成单一业务 App

### 6. No private bypass

新能力不能绕过：
- owner auth
- typed event bus
- async operation
- component manifest
- relay 同步约束

**红线**：为单个组件开私有 transport / auth / sync / runtime，直接否决。

---

## AI 组件规则

AI 能力必须是组件，不是产品身份。

| 组件 | 定位 | 状态 |
|------|------|------|
| `opencode` | 主力 coding-agent | 当前公开参考组件 |
| `codex` | Codex 原生 thread 工作流 | 归档参考 |
| `pilot` | shell/component 状态解释和建议 | 归档参考 |
| `cc` | Advanced backup lane | 归档参考 |

**绝对红线**：
- AI 组件不得默认把首页变成对话入口
- Agent 输出落在组件页面内，ShellHome 只露短 signal
- 不要在 Signals 里放 LLM 生成的摘要或建议

---

## 设计准入门

任何新设计必须先回答以下 7 个问题，写在设计文档开头。

| # | 问题 | 答不清就卡住 |
|---|------|-------------|
| 1 | 这是不是一个 component？如果不是，为什么属于 shell？ | 不能是"辅助功能"或"公共模块"这种模糊回答 |
| 2 | 它是否保持 watcher 的个人服务器终端气质？ | 不能是"让用户更方便"这种空洞回答 |
| 3 | 它会不会把产品主语从 `shell + components` 推向某个具体组件？ | 如果答案含"可能会"，就拆分或降级 |
| 4 | 它的 Android 入口属于 Signals、Tools 还是 System？ | 不能新建第四页 |
| 5 | 它是否新增 transport、auth、sync、runtime 或移动协议？ | 如果是，必须证明为什么不能复用 shell |
| 6 | 它是否依赖自然语言文案表达状态？ | 如果是，必须改成 typed state / typed event |
| 7 | 它是否默认暴露 destructive 或 full-access 动作？ | 如果是，必须降级为 advanced lane |
| 8 | 它是否能被同类 self-hosted owner 按公开文档复现？ | 如果不能，必须补配置示例、安全说明或部署说明 |

**过门标准**：7 个问题全部有明确答案，且没有一条触发红线。

---

## 禁止漂移

以下行为**默认禁止**，不需要讨论：

- [ ] 把首页做成 AI 聊天入口
- [ ] 把 relay 做成业务层
- [ ] 把 shell 做成最大组件
- [ ] 为单个组件开私有 transport / auth / sync
- [ ] 让 Android 直接依赖 service 内部结构体
- [ ] 用自然语言解析驱动移动端状态
- [ ] 在默认入口暴露 full-access / destructive 动作
- [ ] 为了显得智能而增加解释性文案、卡片堆叠和重视觉层级
- [ ] 新增"发现"、"推荐"、"社区"等社交/内容页面
- [ ] 在通知栏放营销文案或表情符号
- [ ] 为功能增加"引导蒙层"或"新手教程"
- [ ] 把本机私有路径、真实 token、私有域名写进公开默认配置
- [ ] 为开源传播增加营销式首页、社区流或 SaaS 注册路径

---

## 组件分类指南

判断一个功能该放哪里的快速决策树：

```
这个功能...
  ├─ 是领域业务逻辑？ → component
  │     ├─ 需要 AI 模型？ → opencode，或未来新 AI component
  │     ├─ 是服务器工具/文件下载？ → host component
  │     └─ 是代码执行/工具？ → 新建 component
  │
  ├─ 是跨组件的契约/调度？ → shell
  │     ├─ 是认证/传输/事件总线？ → shell 核心
  │     ├─ 是设备同步/推送？ → shell 核心
  │     └─ 是诊断/更新？ → shell 核心
  │
  └─ 是纯 UI 展示？ → Android shell
        ├─ 是状态摘要？ → Signals
        ├─ 是功能入口？ → Tools
        └─ 是配置/诊断？ → System
```

**边界案例**：
- "在首页显示 opencode 最近 turn 的状态" → **Signal**（opencode 组件提供事件，shell 决定展示形式）
- "opencode 要求在首页常驻 New Thread 按钮" → **禁止**（组件不能改 shell 布局）
- "新增一个游戏" → **Component**（如 block），通过 Tools 入口进入

---

## 验收规则

一个设计同时满足以下三条，才符合 watcher 格调：

1. **归属清晰**：能被清楚归入 shell 或某个 component，不存在"两者都是"
2. **状态可机读**：状态能通过 typed resource / operation / event 解释，不依赖自然语言
3. **终端轻量**：不会把 Android 壳层从轻量终端推向通用控制台

---

## 修订记录

- **0.1** — 初始版本
- **0.2** — 增加反例/正例、组件分类决策树、AI 组件状态表、红线清单细化
- **0.3** — 增加开源取向：面向同类 self-hosted owner，而不是改成 SaaS 或新手产品
