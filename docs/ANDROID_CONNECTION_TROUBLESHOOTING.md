# Android Connection Troubleshooting

这份文档专门解决 Android app 安装后“连不上 relay”的问题。

## 先说结论

如果你装在真机上，`10.0.2.2` 往往是错的。

- `10.0.2.2`
  只适用于 Android Emulator
- 真机
  要填电脑在局域网里的 IP，或者公网域名

## 地址选择

如果你使用公网 IP 或域名，这条规则也一样：

- 模拟器默认可以尝试 `10.0.2.2`
- 真机要填电脑或服务器真实可访问的 IP / 域名

例如真机里优先尝试：

```text
http://<server-host>:8780
```

而不是固定使用：

```text
http://10.0.2.2:8780
```

## 连接失败的常见原因

### 1. relay 根本没启动

检查：

```bash
curl http://127.0.0.1:8780/api/v1/health
```

如果连不上，就先启动 relay：

```bash
export PATH=/path/to/go/bin:$PATH
cd <watcher-workspace>
go run ./relay/cmd/watcher-relay --config ./relay/config.example.json
```

### 2. relay 只监听了 `127.0.0.1`

默认 [relay/config.example.json](../relay/config.example.json) 是：

```json
"bind_addr": "127.0.0.1:8780"
```

这意味着只有本机能访问，真机永远连不上。

真机调试时，改成：

```json
"bind_addr": "0.0.0.0:8780"
```

可以直接参考：

- [relay/config.lan.example.json](../relay/config.lan.example.json)

### 3. `allowed_hosts` 没放行真机访问用的地址

如果你用 IP 或域名访问 relay，它要出现在：

```json
"security": {
  "allowed_hosts": [
    "127.0.0.1",
    "localhost",
    "YOUR_LAN_IP",
    "YOUR_DOMAIN"
  ]
}
```

例如你打算让真机访问 `http://<server-host>:8780`，那就要把：

```json
"<server-host>"
```

加进 `allowed_hosts`。

### 4. app 里填的是模拟器地址

当前工程默认值来自 [android/gradle.properties](../android/gradle.properties)：

```properties
WATCHER_RELAY_BASE_URL=http://10.0.2.2:8780
```

这适合模拟器，不适合真机。

真机请在 app 的 `Settings` 里手动改成你的局域网 IP 或公网域名。

### 5. service / relay 没一起跑

app 只连 `relay`，所以“测试连接”主要看 relay。

但如果你想真正收到 watcher 产出的消息，还需要 `service` 也在跑，并且任务正常执行。

启动方式：

```bash
export PATH=/path/to/go/bin:$PATH
cd <watcher-workspace>
go run ./relay/cmd/watcher-relay --config ./relay/config.example.json
```

```bash
export PATH=/path/to/go/bin:$PATH
cd <watcher-workspace>
go run ./service/cmd/watcher-service --config ./service/config.example.json
```

## 真机局域网联调建议步骤

1. 复制一份 relay 配置：

```bash
cp relay/config.lan.example.json relay/config.lan.local.json
```

2. 把 `YOUR_LAN_IP` 改成你电脑的局域网 IP。

3. 用这份配置启动 relay：

```bash
export PATH=/path/to/go/bin:$PATH
cd <watcher-workspace>
go run ./relay/cmd/watcher-relay --config ./relay/config.lan.local.json
```

4. 电脑本机检查：

```bash
curl http://127.0.0.1:8780/api/v1/health
```

5. 手机上的浏览器检查：

```text
http://<server-host>:8780/api/v1/health
```

如果手机浏览器都打不开，app 一定也连不上。

6. app 里打开 `Settings`：

- Relay URL 填 `http://<server-host>:8780`
- Owner token 填 relay 配置里的 `owner_token`
- 点 `Test Connection`
- 再点 `Register Device`

## 公网部署注意

如果这是云服务器公网 IP，还要同时确认：

- 云安全组放行 TCP `8780`
- 本机防火墙没有拦 `8780`
- `allowed_hosts` 放行你实际使用的域名或 IP
- 安全加固线仍需按 [Security](SECURITY.md) 和
  [Security Plane](foundation/SECURITY_PLANE.md) 处理
