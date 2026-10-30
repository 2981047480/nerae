# P02-13 · 训练待发操作安全存储底层

## 状态

**partial**。新增 iOS Keychain 与 Android Keystore 的固定第三槽位和 UTS 读/CAS 接口，并以合成后端验证边界。没有接训练 journal、训练页面、账户会话或实际 App 调用点；没有真机证据。

## 接口与边界

- 固定槽名为 `training-operation-journal`，与认证会话及设备身份注册表分别使用独立 Keychain service/account 或独立 Android Keystore alias/加密文件名。现有认证状态清除函数只清认证槽；没有导出训练槽的无条件覆盖或清除 API。
- UTS 仅提供 `readTrainingOperationJournal()` 与 `compareAndSetTrainingOperationJournal(expectedFound, expectedValue, value)`。CAS 区分预期不存在与精确旧文本；冲突不返回槽内数据。iOS Swift 桥多参数均使用 `_` 外部标签，Android 仅接受固定槽，不接收 alias、文件路径或 service 名。
- 值必须是 JSON 对象，UTF-8 明文最多 65,536 字节。精确上限合成值可写；超限、非法 UTF-8/JSON、空值和非对象均失败关闭。该槽不能承载 512 KiB 训练操作；未来 journal 若超限必须由上层明确拒绝/拆分并定义恢复语义，不能假设 vault 自动扩容。
- CAS 在每个平台共享的单 App 进程锁内完成读取、比较和替换；这是单进程原子性，不是跨进程、扩展或多进程 CAS。调用方须复用平台桥的固定 vault 实例/锁。没有事务式文件/Keychain 提交确认接口；若底层写结果未知，返回 unavailable 且不清除槽或密钥，实际保存状态可能是旧值或新值，调用方必须重新读取核对。
- Android 生产加密仍要求硬件支持的 Android Keystore AES-GCM；测试软件密钥仅为 JVM 合成依赖。iOS Keychain 项仍使用设备绑定、解锁后可用的既有访问策略。

## 验证

| 命令 | 结果 |
|---|---|
| `bash docs/evidence/P02/P02-13/verify-ios.sh` | 退出码 0；Swift 合成后端覆盖三槽隔离、CAS 冲突、精确 64 KiB、超限、并发 writer、明确写失败和“写入后异常”的未知结果保留。未访问真实 Keychain。 |
| `bash docs/evidence/P02/P02-13/verify-android.sh` | 退出码 0；Kotlin 源码按 Android API 35 编译，合成后端覆盖相同边界及 Keystore 槽/文件名互异。AES 使用软件测试密钥，未访问设备 Keystore。编译仅有既有 `isInsideSecureHardware` 弃用警告。 |
| `NERAE_KEEP_P02_13_UTS=1 bash docs/evidence/P02/P02-13/verify-android-uts.sh` | 退出码 0；HBuilderX 5.33 转换生成 Kotlin wrapper，确认 wrapper 调用两个训练 journal 方法；未构建 APK/执行 Keystore。目录保留于 `/private/tmp/nerae-p02-13-android-uts.QucF6CjW`。 |
| `NERAE_KEEP_P02_13_IOS_UTS=1 bash docs/evidence/P02/P02-13/verify-ios-uts.sh`（授权本机执行） | 最终退出码 0。纯透传 `xcodebuild "$@"` wrapper 保留参数并 tee 日志；framework 为 iPhoneOS arm64，编译接口包含 `readTrainingOperationJournal()` 与 `compareAndSetTrainingOperationJournal(...)`，Swift CAS 桥三参数无外部标签。SHA-256 `1fa8da51a8c41cf821f9817fb55225e8776b9d889e9f6d85ad3f5b571b60559c`；保留 `/private/tmp/nerae-p02-13-ios-uts.xoHPta5g`。 |

首次 Swift 合成运行因测试将仅 `Equatable` 的 namespace 放入 `Set` 而失败，随后改为显式两两比较并通过。首次 Kotlin 合成运行碰到 compile-only `android.jar` 中 Android `JsonReader` stub 不可在 JVM 使用；合成测试换用独立严格 JSON 解析器，生产实现未改变。

## iOS UTS 失败复核与修复

前两次 iOS UTS 失败原因不同：首次日志确实为共享 DerivedData PIFCache 缺少工作区文件；第二次脚本 wrapper 把 `-derivedDataPath` 强加给每次 `xcodebuild`，包括版本探测，HBuilderX 报最低版本检查失败，而被 wrapper 记录的 `xcodebuild` 输出为 26.6。第二次 wrapper 还改写了构建参数，因此不能仅凭该日志断定 HBuilderX 对原生 Xcode 的状态。已将 wrapper 改成纯透传并只捕获输出；获授权本机环境重跑成功，未清理共享缓存。两项变化（wrapper 与执行环境）同时变更，故“强加参数导致版本误判”是有证据支持的主要解释，但不是完全隔离变量的证明。

成功产物及 Swift interface 保留在 `/private/tmp/nerae-p02-13-ios-uts.xoHPta5g`；这只证明 HBuilderX 5.33 能将该模块编译为未签名 iPhoneOS framework，不代表项目锁定工具链、安装、Keychain 运行或真机行为。

## 限制与接线要求

- 64 KiB 是硬边界。若训练操作 payload 可能超过上限，必须先由训练层定义有界 journal 格式、分批/拒绝策略和未知提交后的恢复核对，不能把此底层称为通用离线队列。
- 目前只保证 App 进程内 CAS；另一 App 进程或扩展同时写入不受该互斥保护。不要把它用作跨进程事务锁。
- 尚未接 journal schema、训练操作状态机、重放/幂等、后台同步、登出生命周期，也未验证 iOS/Android 真机 Keychain/Keystore 行为。不能声称训练操作已可靠排队或跨重启恢复。
- iOS UTS 编译仍待在不清理共享缓存的授权本机环境排障后复验；Android UTS 转换通过不代表 APK、签名、安装或真机行为。
