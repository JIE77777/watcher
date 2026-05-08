# Push Notifications

服务端主动推送通知到客户端，实现低延迟事件唤醒。

## 架构

```
service (事件源)
  │  PublishEnvelope → relay
  ▼
relay (Go, :8780)
  ├── 存储 event envelope (SQLite)
  ├── 查询所有注册设备 (ListDevicesWithPush)
  ├── 并发推送 (DispatchAll)
  │     ├── Xiaomi MiPush HTTP API
  │     ├── Selfhost WebSocket (WSHub)
  │     └── (预留) FCM / APNs / Huawei
  ▼
Android 客户端
  ├── WebSocketPushService → 主动长连接 (selfhost)
  ├── MiPushReceiver → 被动唤醒 (xiaomi, 备用)
  ├── BackgroundSyncWorker → 拉取增量事件
  └── 本地通知展示
```

## 数据流

1. `service` 产生 `WatcherTaskEvent`
2. `service` 调用 `relay POST /api/v2/events/publish` 发布 envelope
3. `relay` 存储 envelope 后自动调用 `dispatchPushForStream`
4. `Dispatcher` 查询所有已注册推送设备，通过对应 provider 并发推送
5. Android 客户端收到推送：
   - **selfhost**: `WebSocketPushService` 收到 JSON `{"type":"push","stream":"...","action":"sync"}`
   - **xiaomi**: `MiPushReceiver.onReceivePassThroughMessage` 被系统唤醒
6. 触发 `BackgroundSyncWorker` 一次性同步，拉取增量事件
7. 客户端展示本地通知

## WebSocket 推送 (selfhost)

### 连接端点

```
GET /api/v2/push/ws?token={device_token}
```

设备通过 query 参数 `token` 或 header `X-Device-Token` 认证。连接建立后服务端发送 welcome 消息：

```json
{"type": "connected", "ts": 1714123456}
```

### 消息协议

**服务端 → 客户端 (推送唤醒):**
```json
{"type": "push", "stream": "codex.operation", "action": "sync"}
```

**服务端 → 客户端 (心跳):**
```json
{"type": "ping", "ts": 1714123456}
```

### 连接管理

- **WSHub** (`internal/push/hub.go`): 内存连接池，按 `device_id` 索引
- 每设备仅保留一个连接，新连接替换旧连接
- `readPump` 检测断开，`writePump` 发送心跳
- 优雅关闭: `hub.CloseAll()` 在 SIGTERM 时关闭所有连接

### 注册方式

```json
{
  "device_id": "abc123",
  "push_token": "ws:abc123",
  "push_provider": "selfhost",
  "platform": "android",
  "device_name": "Pixel 8"
}
```

`push_token` 以 `ws:` 前缀开头，`GuessProvider()` 自动识别为 `selfhost`。

### Android 保活

```
WebSocketPushService (前台服务)
├── OkHttp WebSocket (pingInterval=30s)
├── ConnectivityManager.NetworkCallback → 网络变化立即重连
├── 指数退避 5s→60s (连续失败时)
└── 收到 push → WorkManager OneTimeWork → BackgroundSyncWorker

三层兜底:
  L1: WebSocket push → 即时触发 sync
  L2: WorkManager 15min 定期轮询 → 补漏
  L3: 用户手动刷新 → 最后防线
```

## Provider 矩阵

| Provider | 状态 | 适用场景 |
|----------|------|----------|
| `selfhost` | ✅ 已实现 | 自建 WebSocket 长连接，个人服务器部署 |
| `xiaomi` | ✅ 已实现 | 中国大陆小米设备，MiPush HTTP API |
| `fcm` | 🔲 预留 | 国际 Android (Google Play Services) |
| `apns` | 🔲 预留 | iOS (Apple Push Notification Service) |
| `huawei` | 🔲 预留 | 华为设备 (Huawei Push Kit) |

## API

### POST /api/v2/push/register

设备注册推送 token。需要 device token 认证。

```json
{
  "device_id": "abc123",
  "push_token": "mipush_reg_id_here",
  "push_provider": "xiaomi",
  "platform": "android",
  "device_name": "Xiaomi 14"
}
```

### POST /api/v2/push/notify

手动触发推送。需要 owner auth。

```json
{
  "stream": "watcher.task",
  "action": "sync"
}
```

### 自动推送

`POST /api/v2/events/publish` 发布 envelope 时，relay 自动向所有已注册设备推送，无需额外调用。

## 配置

### Relay config.json

```json
{
  "push": {
    "xiaomi": {
      "app_id": "your_mipush_app_id",
      "app_key": "your_mipush_app_key",
      "app_secret": "your_mipush_app_secret",
      "use_sandbox": true,
      "channel_id": "watcher_push"
    }
  }
}
```

### Android local.properties

```properties
WATCHER_MIPUSH_APP_ID=your_app_id
WATCHER_MIPUSH_APP_KEY=your_app_key
```

### Service config.json (可选 relay_push 独立通道)

```json
{
  "relay_push": {
    "base_url": "http://127.0.0.1:8780",
    "owner_token": "your-owner-token"
  }
}
```

## 文件清单

| 文件 | 作用 |
|------|------|
| `internal/push/hub.go` | WebSocket 连接池 (WSHub) — 注册/注销/推送/心跳/优雅关闭 |
| `internal/push/dispatcher.go` | 推送分发器 — 多 provider 路由、Xiaomi HTTP API、selfhost WebSocket、并发推送 |
| `internal/store/relay.go` | `UpdateDevicePushInfo` / `ListDevicesWithPush` 存储层 |
| `internal/relayclient/client.go` | `NotifyPush` — service → relay 推送触发 |
| `relay/cmd/watcher-relay/main.go` | `/api/v2/push/*` 路由、WebSocket 端点、envelope 发布后自动推送 |
| `relay/config.example.json` | 推送配置示例 |
| `service/cmd/watcher-service/main.go` | `relay_push` 配置、`publishRelayPushEnvelope` / `notifyRelayPush` |
| `service/config.example.json` | `relay_push` 配置示例 |
| `android/.../WebSocketPushService.kt` | WebSocket 前台服务 — 长连接、心跳、网络变化重连、触发同步 |
| `android/.../MiPushReceiver.kt` | MiPush 消息接收、注册回调、触发后台同步 |
| `android/.../WatcherApplication.kt` | MiPush SDK 初始化、WebSocket 服务启动 |
| `android/.../WatcherApi.kt` | `registerPushToken` / `ensurePushTokenRegistered` / `ensureSelfHostPushRegistered` |
| `android/.../BackgroundSyncScheduler.kt` | WorkManager 15min 定期轮询 (兜底层) |
| `android/.../SettingsActivity.kt` | 推送状态展示、手动注册按钮 |

## 设计决策

1. **自动推送** — envelope 发布即推送，不需要 service 显式调用 push，简化业务逻辑
2. **并发分发** — `DispatchAll` goroutine 并发推送所有设备，不因单设备失败阻塞
3. **轻量 payload** — 只推送 `{stream, action}`，客户端收到后主动拉取完整数据
4. **Provider 插件化** — switch-case 路由，新增 provider 只需添加一个 case 分支
5. **MiPush raw=1** — 使用透传消息，客户端自行决定如何展示，避免通知栏样式锁定
6. **WebSocket 一设备一连接** — hub 按 device_id 索引，新连接替换旧连接，防止泄漏
7. **推送失败无害** — 事件已落盘 SQLite，WS 不在线时 WorkManager 轮询兜底
8. **三层兜底** — WebSocket push → WorkManager 定期轮询 → 手动刷新，逐级降级

## Signal Notifications

业务通知不把完整业务 payload 塞进 relay push。relay 仍然只发送轻量
`{stream, action}` wakeup。

Android 收到 push 后会：

1. 拉取 `GET /api/v2/shell/home`
2. 选择最高优先级 `ShellSignal`
3. 生成本地通知
4. 点击通知后按 `ShellSignal.target` 跳转

当前优先级：

- `action_required=true`
- `level=action|warning|error|failed`
- `component_id=opencode`

这让 opencode 的等待回答、权限请求、运行中会话能进入通知栏并直接跳回会话页。
通知去重按 `signal_id + target` 做，本地缓存只用于避免同一条 signal 重复打扰。
