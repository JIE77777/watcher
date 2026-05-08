# Shell Mobile UI

Android 壳层从 `Cockpit` 收敛为三块：

- `Signals`
  首页，只显示组件主动筛过的短消息和后台工作状态。
- `Tools`
  图标式组件格子，负责进入组件。
- `System`
  设置和诊断入口，收纳 relay、更新、设备、worker、runtime、crash、cache。
- `Security`
  独立安全面，负责 transport/auth/public exposure posture，不承载业务模块状态。

## Home Contract

首页 Signals 和壳层状态只读聚合接口：

```text
GET /api/v2/shell/home
```

返回：

- `status`
- `updated_at`
- `signals`
- `components`

`signals` 是组件筛过的短提示，不是 feed/dashboard。`components` 是兼容入口格子，不承载组件业务细节。

Tools 页的 live 模块列表读取：

```text
GET /api/v2/modules
```

未知模块打开通用 module descriptor 页面；已知模块可以继续进入 bespoke 页面。

## Boundaries

- Shell 不复制 Box feed、Codex thread list 或 diagnostics。
- Shell 只理解 `target`，不理解组件内部业务。
- 组件打开后拥有自己的完整页面。
- 运维信息默认进入 `System`，除非组件不可用才在首页露出短状态。
- 安全姿态进入独立 `Security` 页面，不混进模块业务页面。

## Display Config

时间事实仍用 UTC/RFC3339 存储和同步。Android 只在展示层按配置格式化：

- 默认语言：`zh`
- 默认时区：`Asia/Shanghai`
- Android 可在 `System -> Display` 切换 `zh/en` 和 IANA time zone。

服务端与 relay 配置也保留 `display.default_language` 和 `display.timezone`，作为系统默认审计口径；设备本地设置可以覆盖展示。
