# Watcher Docs

`watcher` 的公开文档按三层维护：

- `foundation`
  当前已经落地并冻结的基础架构、边界和约束。
- `modules`
  `watcher` 作为模块化瑞士军刀的功能分层说明。
- `operations`
  环境、安全、连接排查、版本等运行资料。

私有研究、部署记录和历史深挖材料不从 `docs/` 整目录导出。公开文档在私有仓库里按 public-safe 标准维护，导出规则见
[Public Export](foundation/PUBLIC_EXPORT.md)。

## Foundation

- [Architecture](ARCHITECTURE.md)
- [Platform Blueprint](foundation/PLATFORM_BLUEPRINT.md)
- [Shell And Components](foundation/SHELL_COMPONENT_MODEL.md)
- [Shell Contract V2](foundation/SHELL_CONTRACT_V2.md)
- [Module Contract V2](foundation/MODULE_CONTRACT_V2.md)
- [Security Plane](foundation/SECURITY_PLANE.md)
- [Shell Mobile UI](foundation/SHELL_MOBILE_UI.md)
- [Product Tone](foundation/PRODUCT_TONE.md)
- [Open Source Readiness](foundation/OPEN_SOURCE_READINESS.md)
- [Public Export](foundation/PUBLIC_EXPORT.md)
- [Release Lines](foundation/RELEASE_LINES.md)
- [Component Standard](foundation/COMPONENT_STANDARD.md)
- [Tool Standard](foundation/TOOL_STANDARD.md)
- [Component Decision Template](foundation/COMPONENT_DECISION_TEMPLATE.md)
- [Component Acceptance Checklist](foundation/COMPONENT_ACCEPTANCE_CHECKLIST.md)
- [Shell And Component Task Split](foundation/TASK_SPLIT.md)
- [Tool Protocol](TOOL_PROTOCOL.md)

## Modules

- [Watcher Components](../modules/README.md)
- [Component Template](../modules/COMPONENT_TEMPLATE.md)
- [Component Manifest Template](../modules/COMPONENT_MANIFEST_TEMPLATE.json)
- [Box Component](../modules/box/README.md)
- [Host Module](modules/HOST_MODULE.md)
- [Push Notifications](modules/PUSH_NOTIFICATIONS.md)
- [Opencode Agent](modules/OPENCODE_AGENT.md)

Archived reference docs:

- [Codex Component](../modules/codex/README.md)
- [Pilot Component](../modules/pilot/README.md)
- [CC MiMo Component](../modules/cc/README.md)

Archived modules are kept as short references only. Their full historical notes
are private material unless they are intentionally rewritten for the public
mainline.

## Operations

- [Android Connection Troubleshooting](ANDROID_CONNECTION_TROUBLESHOOTING.md)
- [Environment Setup](ENVIRONMENT_SETUP.md)
- [Prebuilt Deployment](PREBUILT_DEPLOYMENT.md)
- [Security](SECURITY.md)
- [Versioning](VERSIONING.md)

## Notes

- `docs/frozen/` 里的文档是私有阶段性冻结快照，不随公开主线导出。
- `foundation/` 里的 shell/component 文档是当前主线；`docs/frozen/` 更偏历史快照。
- 开源主线包含 Watcher 基座与 `opencodev2` 参考模块；安全加固线单独推进，当前文档只固化公开范围和部署姿态。
- `box` 公开为配置驱动的信息源组件；个人实验工具只作为 private box 配置保留，不进入公开导出。
- 壳层当前的正式移动入口是 `GET /api/v2/shell/home`；正式运维面是 `GET /api/v2/shell`、`GET /api/v2/shell/diagnostics`、`GET /api/v2/components`；模块发现入口是 `GET /api/v2/modules` 和 `GET /api/v2/modules/{component_id}`。
- `modules/` 负责说明长期的组件边界，避免产品定义散在聊天记录里。
- `foundation/` 负责说明跨模块稳定边界，避免 relay/service/app 的职责在实现中漂移。
- 现有代码暂不为概念重命名；先用文档把边界钉住，再按模块逐步收拢实现。
