# Watcher Environment Setup

这份文档把当前 `watcher` 主线需要的环境、脚本和工具职责集中写下来，方便以后在新机器上复用，不用再翻聊天记录。

## 总览

`watcher` 当前主线分成三块：

- `service/`：本机任务调度、快照、diff、inbox、投递
- `relay/`：公网轻中继
- `android/`：轻量消息接收器

为了把 Android 端先落地成可用产品，目前最重要的本地环境有：

- Java 17
- Android SDK
- 项目内 `gradlew`
- Go toolchain

## 两条部署路径

- 预构建部署：直接使用发布包里的 `watcher-service`、`watcher-relay`
  和 APK。目标机不需要 Go、Java、Gradle 或 Android SDK；只需要编辑运行配置。
  见 [Prebuilt Deployment](PREBUILT_DEPLOYMENT.md)。
- 源码构建：在本机编译后端和 Android APK。只有这条路径需要本文件下面的
  Go、JDK、Android SDK 和 Gradle 环境。

## Android 环境准备

### 1. Java

要求：

- `JDK 17`

用途：

- Android Gradle Plugin 运行时
- Kotlin/Java 编译

检查：

```bash
java -version
```

### 2. Android SDK

建议安装到：

```text
/path/to/android-sdk
```

最少需要的组件：

- `platform-tools`
  用途：提供 `adb`，负责连接真机、安装 APK、查看设备
- `platforms;android-35`
  用途：提供 Android 35 的编译平台
- `build-tools;35.0.0`
  用途：提供 APK 构建所需的打包工具
- `cmdline-tools`
  用途：提供 `sdkmanager`、`avdmanager` 等命令行管理工具

如果要跑模拟器，再加：

- `emulator`
- `system-images;android-35;google_apis;x86_64`

### 3. local.properties

项目内文件：

- `android/local.properties`

用途：

- 告诉 Android 构建系统 SDK 在哪里

当前内容格式：

```properties
sdk.dir=/path/to/android-sdk
```

### 4. 项目内 gradlew

入口：

- [android/gradlew](../android/gradlew)

用途：

- 统一 Android 构建入口
- 缺系统 Gradle 时自动下载固定版本
- 保证不同机器尽量用一致的 Gradle 版本

常用命令：

```bash
cd <watcher-workspace>/android
./gradlew --version
./gradlew assembleDebug
```

## Android 项目脚本说明

这些脚本都在 [android/scripts](../android/scripts)：

- `doctor.sh`
  用途：检查 Java、SDK、adb、local.properties、gradlew 是否可用
- `prepare-local-properties.sh`
  用途：根据 `ANDROID_SDK_ROOT` 或 `WATCHER_ANDROID_SDK_ROOT` 生成 `local.properties`
- `install-gradle.sh`
  用途：下载并缓存项目使用的 Gradle
- `build-debug.sh`
  用途：构建 debug APK
- `install-debug.sh`
  用途：用 `adb` 把 debug APK 直接安装到设备上；如果 APK 还没生成，会先构建

relay 侧还支持：

- `app_release`
  用途：向 app 发布当前可安装 APK 的版本信息和下载入口，供 `Update App` 按钮使用

推荐日常命令：

```bash
cd <watcher-workspace>/android
./scripts/doctor.sh
./gradlew assembleDebug
./scripts/install-debug.sh
```

## Devtools Android 工具层

目录：

- [devtools/android](../devtools/android/README.md)

这层不是 app 构建必需品，而是项目通用的 Android 逆向/调试/工程辅助工具。

### wrapper / 工具用途

- `bin/android-tool`
  用途：总入口，转发到具体 Android 辅助工具
- `bin/adb`
  用途：统一调用 `adb`
- `bin/apksigner`
  用途：统一调用 APK 签名工具
- `bin/bundletool`
  用途：统一调用 App Bundle 工具
- `bin/apktool`
  用途：统一调用 APK 反编译/资源解包工具
- `bin/jadx`
  用途：统一调用 Java/Kotlin 反编译查看工具
- `bin/apk-inspector`
  用途：统一调用 APK Inspector

### 流程脚本用途

- `scripts/decode_apk.sh`
  用途：解包和反编译 APK
- `scripts/install_apk.sh`
  用途：安装现成 APK
- `scripts/sign_apk.sh`
  用途：给 APK 重新签名

## Go 服务环境

`watcher` 的后端部分依赖 Go。

用途：

- `service/` 编译和运行
- `relay/` 编译和运行
- `go test ./...` 校验主线代码

常用命令：

```bash
export PATH=/path/to/go/bin:$PATH
cd <watcher-workspace>
go test ./...
go run ./relay/cmd/watcher-relay --config ./relay/config.example.json
go run ./service/cmd/watcher-service --config ./service/config.example.json
```

## 主线运行顺序

### 本地调试

1. 启动 relay
2. 启动 service
3. 构建 Android APK
4. 安装到设备
5. 在 app 里填 relay URL 和 owner token
6. 测试连接、注册设备、刷新 inbox

### 常用命令

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

```bash
cd <watcher-workspace>/android
./scripts/doctor.sh
./gradlew assembleDebug
./scripts/install-debug.sh
```

## 当前主线重点

app-first 这条主线当前已经具备：

- 设备内配置 relay
- 测试连接
- 注册设备
- 拉取 inbox
- 本地缓存消息
- 本地通知新增消息

接下来优先级最高的演进通常是：

1. 产出稳定 APK 并安装到真机
2. 接入 FCM 或后台同步
3. 做更完整的消息状态和本地持久化

## 真机与模拟器的 URL 区别

- Android Emulator
  可以使用 `http://10.0.2.2:8780`
- Android 真机
  必须使用电脑的局域网 IP 或公网域名，不能直接用 `10.0.2.2`

真机连接排查可直接看：

- [Android Connection Troubleshooting](ANDROID_CONNECTION_TROUBLESHOOTING.md)
