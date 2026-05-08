# Component Acceptance Checklist

组件进入主仓前，至少满足：

- 通过 [Product Tone](PRODUCT_TONE.md) 的设计入口约束
- 有 `component.json`
- 通过 shell contract `v2` 校验
- 资源、操作、事件流已经命名完成
- Android 入口明确属于 `Signals`、`Tools` 或 `System`
- 不把自然语言文案作为移动端状态机
- 不自建 transport / auth / sync
- Android 只依赖 public `v2` DTO
- README 已说明范围、非目标、shell 依赖
- 有最小测试清单
- 如果是 heavy component，worker 可被 shell 拉起并可健康探测
- 如果是 heavy component，worker 崩溃时进行中的 operation 会被标成 `interrupted`
