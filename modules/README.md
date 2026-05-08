# Watcher Components

`watcher` 现在把长期能力按“组件”组织，并把自己收成一个：

`shell + components` 的模块化瑞士军刀

这里保留 `modules/` 这个目录名，但产品层面的正式概念改成 `component`。

这里的“组件”是 `watcher` 产品自己的能力组件，不等于 `Codex CLI plugin`。

## Current Components

- `opencode`
  主力 coding-agent 组件。通过 Watcher operation/event 模型承载跨设备 coding turns。
- `box`
  配置驱动的信息源组件。通过 `.box.json` 热更新 source / dataset / view / signal。
- `host`
  服务器状态监控和安全文件下载组件。替代 `probe` 作为公开主线的非 agent 示例。

## Private Box Sources

这些工具链不进入公开导出，但已经迁入新的 box 配置框架：

- `modules/box/private/`
  私有 `.box.json`，把现有抓取缓存映射为 dataset/view。
- `tools/scrapers/`
  私有采集实现，后续只作为 source adapter 的数据来源。

## Archived Reference Components

这些组件保留代码和文档，但不属于开源主线、默认 Android 入口或新架构样板。

- `codex`
  历史 Codex app-server/mobile bridge。未来 Codex-v2 如恢复，应按 `opencodev2` 的 conversation projection 和 module contract 重写。
- `pilot`
  壳层语义助手原型。仅参考 shell capsule、deterministic fallback 和 worker-lane 经验。
- `cc`
  Claude Code + MiMo 的受管重会话高级 lane。仅参考 worker orchestration、patch artifact 和 timeout/cancel 经验。
- `probe`
  内部 worker-lane 验证样板。已由 `host` 替代，不再作为公开主线入口。

## Why This Layer Exists

当前仓库里已经有：

- `tools/`
- `service/`
- `relay/`
- `android/`
- `devtools/`

这些是实现层目录，不完全等于产品能力边界。

`modules/` 负责把组件定义钉住，避免出现：

- 功能定义散在聊天记录
- 一个新能力不知道该挂在哪里
- app、relay、service 同时长出跨模块耦合

## Mapping Rule

- 组件定义在 `modules/`
- 壳层基建主要落在 `service/`、`relay/`、`android/`、`internal/`
- 具体实现仍可暂时在现有目录里
- 只有当某个组件稳定后，再逐步把实现向对应组件收拢

## Required Contract

每个组件至少要有：

- 一个 `modules/<component>/README.md`
- 一个 `modules/<component>/component.json`
- 一份 `docs/modules/` 下的运行时或架构文档
- 清晰的 resource / operation / event / state 说明
- 清晰的 capability / surface / default target / action 声明

新增组件时，先看：

- [Shell And Components](../docs/foundation/SHELL_COMPONENT_MODEL.md)
- [Product Tone](../docs/foundation/PRODUCT_TONE.md)
- [Component Standard](../docs/foundation/COMPONENT_STANDARD.md)
- [Shell Contract V2](../docs/foundation/SHELL_CONTRACT_V2.md)
- [Module Contract V2](../docs/foundation/MODULE_CONTRACT_V2.md)
- [Component Decision Template](../docs/foundation/COMPONENT_DECISION_TEMPLATE.md)
- [Component Acceptance Checklist](../docs/foundation/COMPONENT_ACCEPTANCE_CHECKLIST.md)
- [Component Template](COMPONENT_TEMPLATE.md)
- [Component Manifest Template](COMPONENT_MANIFEST_TEMPLATE.json)

## Current Mapping

- `codex`
  已归档。历史实现主要映射到 `service/` 的 runtime/operation、`relay/` 的 typed event bus、`android/` 的线程与交互 UI
- `host`
  当前主要映射到 `service/` 的 host API、`relay/` 的转发、`android/` 的 Host 页面
- `pilot`
  已归档。历史实现主要映射到 `internal/workers`、`service/` 的 operation broker、`modules/pilot/worker.py`
- `opencode`
  当前主要映射到 `internal/opencode`、`internal/store`、`service/` 的 session/turn/event/operation runtime、`android/` 的后续会话 UI
- `box`
  当前主要映射到 `internal/box` 的 catalog provider、`modules/box/examples`、`modules/box/private`、`android` 的通用 Box renderer
- `cc`
  已归档。历史实现主要映射到 `internal/workers`、`service/` 的 session broker、`modules/cc/worker.py`、`android/` 的会话 UI
- `probe`
  已归档。历史实现主要映射到 `internal/workers`、`service/` 的 worker orchestration、`modules/probe/worker.py`

## Release Direction

长期目标是：

- `watcher shell` 可单独发布
- 组件可单独开发

但当前阶段先保持单仓库，优先把契约和边界做稳，而不是急着物理拆分。

## Scaffold

新组件现在优先通过官方脚手架起步：

```bash
cd <watcher-workspace>
go run ./devtools/cmd/component-scaffold --id example --name Example
```
