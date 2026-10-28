# P02-01 本机平台工具盘点

盘点日期：2026-10-28（本机只读检查）。仅记录工具可见性与命令结果；未访问账户、证书、私钥或签名配置。

| 平台 | 已见到的工具/条件 | 当前限制 |
|---|---|---|
| iOS | macOS Darwin 25.5 arm64；`xcode-select -p` 指向 `/Applications/Xcode.app/Contents/Developer`，退出 0；Xcode 26.6（build 17F113），退出 0；`xcodebuild -showsdks` 列出 iOS 26.5 和 iOS Simulator 26.5 SDK | 普通沙箱运行 `xcrun simctl list devices available` 退出 1，受权限/沙箱限制（Operation not permitted / connection refused）；经正常审批的只读 `xcrun simctl list devices available --json` 退出 0，列出 2 个 iOS 26.5 可用模拟器，均为 Shutdown。尚未启动、安装或测试模拟器；未执行原生构建、未检查签名凭据 |
| Android | `~/Library/Android/sdk` 存在；`platforms/android-35`、`platform-tools/adb`、`emulator`、`cmdline-tools/latest/bin/sdkmanager` 存在；adb 1.0.41 / 37.0.0-14910828 退出 0；emulator `-list-avds` 退出 0，显示一个本地 AVD 配置 | SDK Build Tools 目录不存在；`sdkmanager --version` 退出 1，当前未能定位 Java Runtime；Android Studio 标准安装位置未见；未启动 AVD、未执行原生构建、未查看签名材料 |
| uni-app / 微信 | Node 26.0.0、pnpm 11.5.3；锁定依赖可离线冻结安装；uni CLI 成功编译 H5、小程序与 App 资源 | HBuilderX 与微信开发者工具在常见 `/Applications` / `~/Applications` 路径未见；工程未配置微信 AppID，也没有真实微信设备验收证据 |

## 检查边界

Xcode/SDK 与 Android SDK 目录存在，不等于原生工程可构建或可签名。普通沙箱下 simctl 查询失败属于权限/沙箱限制；经授权查询虽列出 2 个已关机模拟器，但未启动、安装或测试。小程序和 App 资源编译不等于平台包或真机验收。没有检索、复制、读取或上传任何证书/私钥；没有使用真实账户。
