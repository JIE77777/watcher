# Runtime Tools

`tools/` 只放 `watcher` 运行时会调用的消息采集能力：

- `scrapers/`：网页和榜单抓取
- `connectors/`：需要登录态或 token 的来源连接器
- `parsers/`：通用解析和归一化

首轮公开导出不包含当前私有 `scrapers/` 和历史 adapter。公开 example
component 后续会配套一组干净 fixture 和示例 tool。

这层的约束很明确：

- 输出必须是 `SourceSnapshot`
- 由本机 `service` 以子进程方式调用
- 成功时 `stdout` 只输出 JSON
- 诊断信息写到 `stderr`
- 稳定身份字段不能漂移：`source_id`、`item_key`、`thread_key`、`version`
- 不放 APK 逆向、Android 打包、ADB 之类工程辅助脚本

Android 工程辅助工具统一放到顶层的 `devtools/`。

完整规范见：

- [Tool Standard](../docs/foundation/TOOL_STANDARD.md)
- [Tool Protocol](../docs/TOOL_PROTOCOL.md)
