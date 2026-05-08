# Tool Protocol

Watcher 的 tool 是数据型组件的事实探针，由本机 `watcher-service` 以子进程方式调用。

更完整的开发规范见 [Tool Standard](foundation/TOOL_STANDARD.md)。

首轮公开导出不包含当前私有 `box` 工具链。这里保留的是后续 public
example component 可以复用的基础协议。

## Input

- 调用方式：`<runtime> <entry_point> --config <path>`
- 配置文件是 JSON。
- `service` 会把 `task_id`、`task_name`、`task_labels` 注入到 tool config。
- tool 不读取 relay、Android、owner token 或 shell 私有状态。

推荐任务配置包裹方式：

```json
{
  "tool_config": {
    "source_url": "https://example.com/feed.json"
  },
  "rule_options": {
    "ignore_fields": ["ranking"]
  }
}
```

实际传入 tool 的 config 会被 service 展开为 tool 自己可读的扁平 JSON。

## Output

tool 必须把一个 `SourceSnapshot` JSON 输出到 `stdout`：

```json
{
  "source_id": "example_feed",
  "task_id": "task_xxx",
  "fetched_at": "2026-04-23T09:16:24Z",
  "version": "v1",
  "items": [
    {
      "item_key": "item-1",
      "thread_key": "example_feed:item-1",
      "title": "Example Item",
      "identity": {"id": "item-1"},
      "data": {"status": "changed", "score": 37},
      "external_url": "https://example.com",
      "labels": ["example"]
    }
  ],
  "raw_meta": {}
}
```

稳定性约束：

- `source_id` 必须等于 `manifest.id`。
- `task_id` 必须等于当前 task。
- `version` 表示输出合同版本。
- `item_key` 必须稳定。
- `thread_key` 应稳定；缺省时 service 会用 `task_id:item_key` 兜底。

失败约束：

- 非 0 退出码表示采集失败。
- 诊断信息写到 `stderr`。
- `stdout` 成功时只放 JSON，不夹杂日志。
- `service` 负责做 diff、生成 `watcher.task`、投递通知。
