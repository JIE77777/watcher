# Release Lines

`watcher` 从现在开始明确拆成两条发行线：

- `shell release line`
- `component release lines`

当前仍保持单仓，但版本和产物语义已经分开。

## Shell Release Line

shell 的正式元数据来源：

- [VERSION](../../VERSION)
- [watcher.shell.json](../../watcher.shell.json)

它表达：

- 当前 shell 版本
- 当前 contract version
- 当前 release channel
- 轻 / 重组件默认运行形态

## Component Release Lines

每个公开组件都拥有自己的 release line：

- [modules/box/component.json](../../modules/box/component.json)
- [modules/opencode/component.json](../../modules/opencode/component.json)
- [modules/host/component.json](../../modules/host/component.json)

归档组件保留 manifest 作为历史状态说明。

它们表达：

- 组件版本
- 组件阶段
- shell contract compatibility
- runtime shape
- 组件依赖哪些 shell 能力

## Current Posture

当前发行姿态冻结为：

- 生态边界：`public_preview`
- shell release line：`shell`
- component release lines：`component-box`、`component-opencode`、`component-host`

这意味着：

- 现在就可以把壳子和组件看作不同发行对象
- 但还不急着拆成多仓库或公开市场

## API Surface

为了让这条发布线可被客户端和诊断工具读取，当前已经有：

- `GET /api/v2/shell`
- `GET /api/v2/components`

以后 app 显示“当前 shell 版本 / 已安装组件 / 组件阶段与状态”时，以这两条接口为准。
