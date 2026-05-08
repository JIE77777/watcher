# Connectors

需要登录态、cookie、token 或设备认证的采集源放在这里。

约束：

- 只负责“连上来源”，不直接生成消息事件。
- 最终仍然要输出符合 `SourceSnapshot` 协议的数据。
- 复杂连接逻辑优先做成可复用模块，再被具体 tool 调用。

