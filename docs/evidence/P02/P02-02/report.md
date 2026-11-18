# P02-02 iPhone 构建与签名进度

## 2026-11-18 资源编译（partial，未签装）

- 状态：partial，未接受。基线 `cc76a602790050127405030c07cd78d423ae1eec`（PR #2 已合并进 main）。执行者是 Cursor cloud agent（Grok 4.7）。环境是 Linux x86_64、glibc 2.39、Node v22.23.4、pnpm 11.5.3。`python3 scripts/plan.py ready` 列出 L01-02、L01-03、L01-04、P02-02、P02-03。L01 是外部取证。本步做 P02-02 的 App 资源编译；P02-03 只共享这一步的 Kotlin 源文件，没有单独做 APK。没有改 U02-04 的产品行为。
- 失败现场：`@dcloudio/uni-uts-v1@3.0.0-5030320261023001` 已在 `apps/client/package.json` 和 `pnpm-lock.yaml`。`pnpm install --frozen-lockfile` 之后，`require.resolve('@dcloudio/uni-uts-v1')` 能找到包，但 `require` 抛出 `Cannot find module '@dcloudio/uts-linux-x64-gnu'`。该可选包的 `package.json` 写着 `"libc": ["gnu"]`，pnpm 11.5.3 用 detect-libc 的 `glibc` 比较，判定不兼容并跳过。`uni build -p app` 因此打印 `Cannot find module: @dcloudio/uni-uts-v1`，进程退出码是 0，且没有 `app-service.js`。退出码不能当作这次构建成功。
- 改动：`pnpm-workspace.yaml` 的 `supportedArchitectures.libc` 设为 `current` 和 `gnu`。锁文件没有改。`current` 仍按本机选择 glibc 或 musl；`gnu` 用来接受 DCloud 的标记。macOS 上 darwin 绑定没有 libc 字段，这条设置不替换它们。
- 安装：删掉 `node_modules` 后执行 `pnpm install --frozen-lockfile`，退出 0，安装 530 个包。`@dcloudio/uts` 下出现 `@dcloudio/uts-linux-x64-gnu` 链接，直接加载 `@dcloudio/uni-uts-v1` 成功。若保留改配置前的 `node_modules`，同一命令会显示 Already up to date，不会补上这个包。
- 资源构建：`pnpm --filter @nerae/client build:app` 退出 0，编译器自报 5.33（vue3），并打印 `DONE Build complete`。`apps/client/dist/build/app/app-service.js` 为 403401 字节，SHA-256 `e64b8e94f336f3dc976772651e24428304e00d82de9917066cb206ea820aeaf3`。包内含 `neraeDeviceKey`、`neraeRandom`、`neraeHttps`、`neraeSecureVault` 四个 `initUTSProxyFunction` 代理。同目录 `uni_modules` 有 17 个 Swift 文件和 13 个 Kotlin 文件。产物被 gitignore，不是可安装的 ipa 或 apk，也没有链到 DCloud 离线 SDK。
- 模拟器：未跑。本机没有 `xcodebuild`、`xcrun`、`simctl` 或 Xcode。
- 真机：未跑。没有 iPhone，没有 Android 设备，没有 `adb`。没有签名身份、描述文件或登录的 Apple 团队。
- 仍缺的外部条件：DCloud iOS AppKey（`apps/client/src/manifest.json` 的 `app-plus` 仍是空对象，AppID 仍是 `__UNI__9D1F11A`）、与该 AppID 匹配的 Bundle ID、免费 Apple Personal Team 在 Xcode 中的登录、开发证书和描述文件、5.33 iOS 离线 SDK 的未签名或签名链接、Android 离线 SDK / JDK 以外的 Android SDK、Android 签名和真机。GitHub Foundation 本轮没有跑，不能写成通过。资源编译不是界面验收。
- 下方 2026-11-15 记录是当时 Mac 上的未签名诊断链接，不覆盖本段。

- 2026-11-15：在原独立 SDK 诊断工程放入本轮复制、移除恢复和新的 iOS 草稿存储源码，以独立 DerivedData 重建。第一次干净构建退出 65：样例工程仍指定已移除的 `AppIcon` 资源；第二次仅对诊断构建传入 `ASSETCATALOG_COMPILER_APPICON_NAME=`，未签名构建退出 0。输出主程序为 arm64，包内 `app-service.js` SHA-256 为 `cfb29e2aa00631d67a0932c396b8b94bfe316138ce0902ac10ad8c48c80c077b`，与本轮 App 资源一致；新存储保护符号存在于 `HBuilder.debug.dylib`。SDK 自带库仍有缺少构建时调试模块的警告。两次脱敏构建日志保留于 `/private/tmp/nerae-u01.yKzJLI/xcode-local-undo-unsigned*.log`。此结果只证明诊断工程可编译链接，未签名、未安装、未验证 AppKey 或真机运行。
- 状态：partial；Project Lead，`gpt-6.1-terra`，推理等级未由运行时报告。源码基线为 `a8cd57fd69261871d59c8281f3e5c9ef73428c4b` 加本轮未提交的 U01-01 改动。
- 用户提供的 5.33 离线 SDK 压缩包 SHA-256 与先前记录一致；检查了 8,114 个归档路径及解压空间，在本轮独立临时目录解压。5.33 客户端 App 资源与四个生成的 iOS UTS 插件装入隔离 SDK 工程，本地 CocoaPods Core 加四插件安装退出 0。
- 使用 `xcodebuild -sdk iphoneos -configuration Debug -destination generic/platform=iOS CODE_SIGNING_ALLOWED=NO` 对该工程编译退出 0。汇合登录及训练组卡改动后已重新放入当前 App 资源并重跑，仍退出 0；产物为 arm64，包内仅有一份 `app-service.js`，其 SHA-256 为 `240c01154e4a24d1d0ec7c4cfc59c3ba1db82cde94c48fafe5bf4a61a4f23a70`，与当前汇合的 App 资源一致。四个插件生成源码与此前检查一致，其符号在此前同一诊断工程的链接产物中可找到。SDK 自带库产生缺调试模块和弃用警告。
- 该工程仍是**未签名诊断工程**：使用 SDK 样例工程及仅供本地检查的 Podspec 元数据；四个空配置只保留一份，共享辅助源码只编译一份。实际插件资源查找、隐私与能力配置、正式打包流程和 AppKey 均未验收。不能把此产物交付或安装为可用 Nerae。
- iPhone 已被 Xcode 识别，开发者模式开启；可见一组有效 Apple 开发签名身份。首次自动签名因 SDK 样例内置手动描述文件冲突而失败；清除样例配置后再次签名，Xcode 返回开发团队没有登录账号、找不到该开发 Bundle ID 的描述文件。尚未成功签名、安装、启动或验证 Keychain。
- 本轮为开发签名选用 `dev.nerae.personal.ios9d1f11a`；该标识仅用于个人开发，DCloud 离线 AppKey 需与之及项目 AppID 匹配。SDK 样例自带的 AppKey 不视为 Nerae 的有效配置。用户已收到 Xcode 登录与 AppKey 准备提示，不要求在聊天中提供凭据。
- 临时日志：本轮隔离目录的 `pod-install.log`、`xcode-unsigned.log` 和 `xcode-signing-probe-2.log`。临时目录可被系统清理；最终接受须在可复现工程、同一集成版本和真实设备上重新留证。
