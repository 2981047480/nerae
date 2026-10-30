# P02-12 独立设备身份注册表 vault

## 范围与实现

新增固定用途的 `readDeviceIdentityRegistry` / `compareAndSetDeviceIdentityRegistry` UTS API；没有通用路径、alias 参数、无条件 registry 写入口或 registry 清除操作。CAS 显式区分预期不存在（`expectedFound=false` 且 `expectedValue` 必须为空）与预期原值。冲突统一返回 `ok=false,errorCode=conflict,found=false,value=""`，不返回旧 registry 内容。平台缺省仍返回 `unsupported_platform`。认证态接口与既有数据格式保持不变。

- iOS 使用单独 Keychain service/account：`com.nerae.device-identity-registry.v1` / `device-identity-registry`；访问属性继续为 `WhenUnlockedThisDeviceOnly`。认证态继续使用既有 service/account。
- Android 使用单独 no-backup 文件名和 Android Keystore alias；与认证态分别持有固定槽位、独立进程锁。认证态文件名/alias 保持原值。AES-GCM、硬件校验、严格有界 JSON 和失败关闭逻辑复用现有 vault 实现。首次写入失败时仅当存储再次读取确认仍无密文，才删除本次新建的 key；结果不确定时保留 key/密文状态并失败关闭。
- 两平台均在 registry 对应 vault 的进程级锁内完成读、比较、写；合成竞争测试证明同进程恰有一个同基线 writer 获胜。该保证不覆盖多进程、扩展进程或其他共享该 Keychain/file 的写者；未来出现这些使用方式前须另行设计跨进程原子性。没有把 registry 接入登录、设备注册流程或登出协调器。

## 本轮 CAS 修复与验证

- `bash docs/evidence/P02/P02-12/verify-swift.sh` — **exit 0**。Swift 合成后端验证分槽、两个并发初始化仅一位成功、已初始化状态下竞争写者仅一位成功、冲突不改变值、登出隔离、首次写失败后可重试及旧值写失败保留；未访问真实 Keychain。
- `bash docs/evidence/P02/P02-12/verify-kotlin.sh` — **exit 0**。Corretto 17 + Kotlin 1.9.24、Android API 35 编译生产 Kotlin；软件 AES 合成测试覆盖与 Swift 相同的 CAS 情况及首次写失败后清理未提交的新 alias、重试成功。编译有既存 `isInsideSecureHardware` 弃用警告。
- `bash docs/evidence/P02/P02-12/verify-android-uts.sh` — **exit 0**。HBuilderX 5.33 生成 Android UTS wrapper，检查调用 `compareAndSetDeviceIdentityRegistry` 且不存在无条件 registry 写桥接；没有运行 dex、APK 或 Android Keystore。
- `git diff --check` — **exit 0**。
- 第一轮新增测试曾因断言仍期待竞争写前的值而失败，退出码为 133（Swift）与 1（Kotlin）；修正断言后上述完整重跑通过。失败现场保留：`/private/tmp/nerae-p02-12-swift.qiucVXCq`、`/private/tmp/nerae-p02-08-vault.ZpIlMJxA`。没有清理这些目录。

此前基线证据仍适用：`bash docs/evidence/P02/P02-08/verify-kotlin.sh` 与 `bash docs/evidence/P02/P02-07/verify-synthetic.sh` 曾分别 exit 0；本轮未重跑。

`bash docs/evidence/P02/P02-12/verify-ios-uts.sh` 此前 exit 1：HBuilderX 5.33 / iPhoneOS SDK 26.5 的 framework 构建被 Xcode PIF 缓存缺失阻断，报告 `Could not compute dependency graph` 及缺失的 `WORKSPACE@v11...-json` 路径。本轮未重跑该步骤，也未删除/清理 DerivedData 或 Xcode 缓存。

第一次 iOS UTS 失败现场：`/private/tmp/nerae-p02-07-uts.eyvzWXU9`（脚本前缀来自初始脚本复制时尚未改名）；隔离 HOME 的第二次失败现场：`/private/tmp/nerae-p02-12-uts.A0QOQUSC` 与 `/private/tmp/nerae-p02-12-home.2fQZZL01`。失败目录保留供诊断。

## 未验证与风险

没有在真实 iPhone Keychain 或 Android Keystore/TEE/StrongBox 上执行；没有真机/模拟器安装，也没有 APK/IPA。Android 主机 JVM 的软件 AES 只验证加解密与进程锁语义，不证明硬件密钥行为。iOS Swift 源码通过本机 `swiftc` 合成测试，但此次 HBuilderX 生成的 iPhoneOS framework 未能完成。CAS 仅对本 App 进程内通过该静态 vault 实例发起的调用原子；不覆盖多进程并发。真实平台原生 CAS、调用方集成、设备注册生命周期和真机行为仍待后续验证。

## Lead 集成复验

在集成代码 `16febdd` 加上 Android alias 检查修复的工作树中，`NERAE_KEEP_P02_12_SWIFT=1 bash docs/evidence/P02/P02-12/verify-swift.sh` 与 `NERAE_KEEP_P02_08_VAULT=1 bash docs/evidence/P02/P02-12/verify-kotlin.sh` 均退出 0；夹具分别保留在 `/private/tmp/nerae-p02-12-swift.yr7KM9gR`、`/private/tmp/nerae-p02-08-vault.69XvfBhG`。`python3 scripts/quality/verify_uts_plugin_build.py` 以锁定 5.26 编译并检查 CAS/读取及认证 vault 的 iOS/Android wrapper 和小程序回退，退出 0。后者仅生成源码，未链接 iOS framework 或验证真机。

## iOS framework 接缝修复

先前两次构建停在 Xcode PIF 缓存，无法证明桥接可编译。Lead 采用获授权的本机 Xcode 环境和透传 `xcodebuild` 输出的临时包装器复试，没有清理用户缓存。该次实际进入 Swift 编译，退出 1：生成的 UTS 调用没有参数标签，而 registry CAS 的 Swift 桥声明了后两个标签。完整诊断保留在 `/private/tmp/nerae-vault-xcode-probe.YIEu9Z0G`、`/private/tmp/nerae-p02-12-uts.uijQxh9w`。

将 Swift 桥三个参数改为无外部标签后，以同样的授权环境及保留证据选项重跑 `verify-ios-uts.sh`，退出 0；HBuilderX 5.33.2026102401 / iPhoneOS SDK 26.5 产生 unsigned iPhoneOS arm64 framework，SHA-256 `b8bf4245ab276b374c1f46c2444b5bd19745ed0be8d096f85b463ea2d6ec5d3a`，保留 `/private/tmp/nerae-p02-12-uts.WE8L8yG3` 与 `/private/tmp/nerae-vault-xcode-fixed.XSeEopMS`。这证明该版本可链接 framework，不代表锁定 5.26 的原生 SDK 已验证、签名安装或真实 Keychain 已运行。
