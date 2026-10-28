# P02-01 平台探针报告

状态：success（平台探针与条件盘点已完成，任务接受以任务图为准；原生打包和设备仍未验证）。锁定源码的依赖安装与三端资源构建通过；普通沙箱下 iOS 模拟器查询受权限/沙箱限制；经授权只读查询可列举 2 个 iOS 26.5 模拟器，均处于 Shutdown，尚未启动、安装或测试。Android `sdkmanager` 当前未能定位 Java Runtime，SDK Build Tools 目录不存在。原生包、签名、设备及业务验收均未验证。

执行：`/root/foundation_review`，`gpt-6-luna` / `medium`；Lead 复核平台判断并通过受控只读查询澄清模拟器权限限制。

## 输入与复现

- 源码基线：`4d15f5b39ca9f976a251c167cc3cdfbe9ff69d67`（F01-03 accepted 集成树）；源工作树检查时 clean。
- 使用 `git archive 4d15f5b39ca9f976a251c167cc3cdfbe9ff69d67` 解包到全新 `/private/tmp/nerae-p02-01.jVtWMA`。未复用旧 F01 探针配置，也未将构建产物写入工作树。
- 运行时：Node `v26.0.0`、pnpm `11.5.3`。根锁 SHA-256：`b0a4cee8c2dc9aec7c91dbd8af096a21a89d87645d585c5090653889a1115167`。

| 命令 | 退出码 | 证据 |
|---|---:|---|
| `pnpm install --offline --frozen-lockfile --ignore-scripts --store-dir /private/tmp/nerae-pnpm-store` | 0 | 锁文件当前；506 包从缓存复用，0 下载 |
| `pnpm peers check` | 0 | No peer dependency issues found |
| `pnpm --filter @nerae/client build:h5` | 0 | H5 build complete |
| `pnpm --filter @nerae/client build:mp-weixin` | 0 | 小程序 build complete；CLI 提示导入微信开发者工具，未实际导入 |
| `pnpm --filter @nerae/client build:app` | 0 | App build complete；CLI 提示导入 HBuilderX，未实际导入 |
| `test -s apps/client/dist/build/h5/index.html` | 0 | 存在，473 bytes |
| `test -s apps/client/dist/build/mp-weixin/app.js` | 0 | 存在，267 bytes |
| `test -s apps/client/dist/build/app/app-service.js` | 0 | 存在，1184 bytes |

资源 SHA-256：H5 `index.html` `e2b4b577819a75edc14bdcb346a7321694d534321357f470ba2c0f1e4e733ec9`；小程序 `app.js` `f1de5430e9dcb708f3571b65447f53e0160062620f7d7588c35147a73467e8e9`；App `app-service.js` `6a78ddcd4137b396f552c85902e240728428644b3ac1fbb2923ba26e5e0e8886`。

## 平台工具只读盘点

- macOS Darwin 25.5 arm64；`xcode-select -p` exit 0，Xcode 26.6 / build 17F113 exit 0；Xcode 列出 iOS 26.5 与 Simulator 26.5 SDK。普通沙箱执行 `xcrun simctl list devices available` exit 1，受权限/沙箱限制（Operation not permitted / connection refused）；经正常审批只读执行 `xcrun simctl list devices available --json` exit 0，列出 2 个 iOS 26.5 可用模拟器，当前均为 Shutdown，未启动、安装或测试。
- Android SDK 在 `~/Library/Android/sdk`，Android platform 35、adb 与 emulator 文件存在。adb 37.0.0 exit 0；`emulator -list-avds` exit 0，存在一条本地 AVD 配置（未启动）。`sdkmanager --version` exit 1：当前未能定位 Java Runtime；SDK `build-tools` 目录不存在。Android Studio 常见安装路径未见。
- HBuilderX 与微信开发者工具常见安装路径未见；工程未配置微信 AppID。没有读取或复制证书/私钥，没有使用或访问真实平台账户。

## 未验证与风险

登录/账号绑定方式尚待合同和实际平台材料收敛。通知权限与推送、键盘、安全区、登录回跳、应用挂起/杀进程/重启、断网恢复、跨端同步和撤权缓存失效均未在真实平台或设备验证。平台账号、实际 AppID/Bundle ID、签名、开发者工具导入和商店审核均未核实；工程未配置微信 AppID。

本报告证明的是冻结锁上的资源编译，不证明 iOS/Android 原生安装包、签名、真机启动或模拟器启动/安装/测试、微信开发者工具验收、平台合规或业务已实现。平台核验清单见 `docs/plans/app/P02-01-platform-checklist.md`；主机工具盘点见 `infra/app/P02-01-tooling-inventory.md`。
