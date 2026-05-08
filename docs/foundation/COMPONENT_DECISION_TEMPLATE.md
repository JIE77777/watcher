# Component Decision Template

在创建新组件或大改组件前，先回答这组问题。

设计入口必须先符合 [Product Tone](PRODUCT_TONE.md)。如果组件会把 `watcher` 从“个人服务器终端”推向“AI Agent 菜单”“移动后台控制台”或某个具体组件的附属物，先停止设计。

## Product Gate

1. 这个能力为什么属于 `watcher = shell + components`？
2. 它是 `shell` 能力还是 `component` 能力？为什么？
3. 它是否保持 owner-first、quiet utility、event/operation driven 的格调？
4. 它的 Android 入口属于 `Signals`、`Tools` 还是 `System`？
5. 它是否会让首页变成 feed、chat、dashboard 或控制台？如果会，如何收敛？
6. 它是否默认暴露 destructive / full-access 动作？如果会，如何降级为 advanced lane？
7. 它是否依赖自然语言文案表达状态？如果会，如何改成 typed state / typed event？

## Component Gate

1. 组件 ID 是什么，为什么不属于现有组件？
2. 组件属于 `light` 还是 `heavy`，为什么？
3. 正式资源是什么？
4. 正式操作是什么？
5. 正式事件流是什么？
6. Android surface 是否存在？如果存在，最小入口是什么？
7. 哪些能力必须依赖 shell，而不能在组件内自建？
8. 非目标是什么？
9. 这个组件的失败模式是什么，shell 如何观测？
10. 这次接入是否会给 shell 引入新的长期负担？

## Stop Conditions

任一情况出现时，不进入实现：

- 说不清属于 shell 还是 component。
- 需要自建 transport / auth / sync。
- Android 必须理解 service 内部结构体才能工作。
- 状态只能靠自然语言文案解析。
- 默认入口需要 full-access 或 destructive 权限。
- 组件会改变 `watcher` 的产品主语，而不是作为能力挂在 shell 上。
