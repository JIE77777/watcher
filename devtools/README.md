# Devtools

`devtools/` 放工程辅助工具，不直接参与 `watcher` 的消息采集链路。

目前拆分为：

- `android/`：APK/Android 相关辅助工具和 wrapper
- `smoke/`：本机或 relay API 的工程烟测脚本

设计原则：

- 外部已经安装好的系统工具，优先通过 wrapper 接入，不重复拷贝大二进制。
- 项目专用脚本、批处理流程、检查脚本，直接放在这里继续追加。
- 运行时工具和工程工具严格分层，避免 `tools/` 和 `devtools/` 混用。
