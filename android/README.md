# Android MVP

这个目录是 `watcher` 的 Android 轻客户端骨架。

当前实现范围：

- 首页是 `Watcher Cockpit`：relay/device 摘要、Shell/component 快照、Pilot brief、Codex runtime 摘要、watcher.task feed 分区
- 首页 task feed 分区固定走 `GET /api/v2/events/since?streams=watcher.task`
- 首页 `Pilot` 卡片手动调用 `POST /api/v2/modules/pilot/briefs/start`，默认 `provider=auto`
- `Pilot` provider 不可用或额度不足时，首页展示 deterministic fallback brief，不暴露完整错误堆栈
- 首页 `MiMo 会话` 创建 Pilot 持久 chat session，并通过 `chat.turn` 使用 MiMo pro 做壳层自由调度
- 首页 `CC Sessions` 管理 Claude Code + MiMo 会话，默认 full-access 模式
- 应用内配置 relay URL 和 owner token
- 设置页测试 relay 连通性
- 设置页支持信任自签 HTTPS relay 证书指纹
- 设置页 `Update App` 按钮
- 应用内注册设备并保存 `device_token`
- 手动刷新 `GET /api/v2/events/since?streams=watcher.task`
- 基于 WorkManager 的后台周期同步
- 本地缓存最近 watcher.task 事件
- 首次同步后，对后续新增 watcher.task 事件弹本地通知
- 列表页 + 详情页 + 设置页
- `Codex` 会话列表
- `Codex` thread 列表
- `Codex` thread 详情 / turns / operations
- 通过 async operation 发起新 thread、start turn、steer、review、interrupt
- `Opencode` session 列表、项目路径选择、turn 启动、event 轮询、permission resolve、worktree discard
- `Host` 服务器状态和 allowlist 文件下载

暂未接入：

- FCM 推送
- 已读状态回写
- SQLite 本地数据库缓存

使用方式现在是：

1. 安装 app
2. 打开 `Settings`
3. 填 relay URL 和 owner token
4. 如果 relay URL 是自签 `https://...`，先点 `Trust HTTPS Certificate`
5. 点 `Test Connection`
6. 点 `Register Device`
7. 回到首页点一次 `Refresh`
8. 首页点 `Ask Pilot` 可以手动生成一次 shell brief
9. 首页点 `MiMo 会话` 可以打开 Pilot 自己的连续调度会话
10. 首页点 `CC Sessions` 可以打开 Claude Code + MiMo 的受管会话
11. 首页点 `Codex Threads` 可以查看服务器上的 thread 并继续工作
12. 首页点 `Opencode` 可以打开 opencode session，并从 Android 发起受管 turn
13. 后台会继续按系统允许的节奏做周期同步，目标周期约 `15` 分钟

注意：

- `10.0.2.2` 只适合 Android Emulator
- 真机要填电脑的局域网 IP 或公网域名
- 如果是局域网真机测试，relay 不能只监听 `127.0.0.1`
- 没有域名时可以使用 relay 内置自签 HTTPS，端口不必是 `443`
- 设备注册后，日常请求优先使用本机 `device_token`，owner token 只用于注册和恢复
- 后台同步依赖 Android 的 `WorkManager` 调度，不是实时推送；系统省电策略可能延后执行
- `Codex` 页面当前是前台交互面，不做后台长连或流式展示

详细排查见：

- [Android Connection Troubleshooting](../docs/ANDROID_CONNECTION_TROUBLESHOOTING.md)

命令行构建现在优先走项目内的 `gradlew`：

```bash
cd <watcher-workspace>/android
./gradlew assembleDebug
```

`android/gradle.properties` 里的：

- `WATCHER_RELAY_BASE_URL`
- `WATCHER_OWNER_TOKEN`
- `WATCHER_BUILD_WATERMARK`

现在只作为默认值，不再是必须改源码才能用。

`WATCHER_BUILD_WATERMARK` 只显示在 Settings 和 debug report，适合个人私用
构建标记。它不是安全能力，APK 更新信任仍然取决于 Android 签名证书。
个人私用可以沿用 debug 签名；公开发布或长期分发应使用独立 release
keystore，并一直保留同一个签名 key。

## Build Helpers

项目里现在带了几个脚本：

- `./scripts/doctor.sh`
- `./scripts/prepare-local-properties.sh`
- `./scripts/install-gradle.sh`
- `./scripts/build-debug.sh`
- `./scripts/install-debug.sh`

典型流程：

```bash
cd <watcher-workspace>/android
export ANDROID_SDK_ROOT=/path/to/android-sdk
./scripts/doctor.sh
./gradlew assembleDebug
./scripts/install-debug.sh
```

这台机器现在已经把环境补齐并成功构建过 debug APK，构建入口和本地 Gradle 下载流程也已经固定下来。产物在：

```text
<watcher-workspace>/android/app/build/outputs/apk/debug/app-debug.apk
```

## In-App Update

现在 relay 可以暴露当前 APK 的版本信息和下载接口，app 里也有 `Update App` 按钮。

注意：

- 第一次把“带更新按钮的版本”装到手机上，仍然需要手工安装
- 当前发布版本由 relay 的 `app_release` 配置决定
- app 只有在 relay 发布版本的 `version_code` 高于本机已安装版本时，才会真正触发下载安装
- 现在设置页会明确显示“已安装版本 / 已发布版本 / 是否有可用更新”
