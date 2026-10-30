# P02-12 独立设备身份注册表 vault

## 范围与实现

新增固定用途的 `readDeviceIdentityRegistry` / `writeDeviceIdentityRegistry` UTS API；没有通用路径、alias 参数，也没有 registry 清除操作。平台缺省仍返回 `unsupported_platform`。认证态接口保持原有名称和结果结构。

- iOS 使用单独 Keychain service/account：`com.nerae.device-identity-registry.v1` / `device-identity-registry`；访问属性继续为 `WhenUnlockedThisDeviceOnly`。认证态继续使用既有 service/account。
- Android 使用单独 no-backup 文件名和 Android Keystore alias；与认证态分别持有固定槽位、独立进程锁。认证态文件名/alias 保持原值。AES-GCM、硬件校验、严格有界 JSON 和失败关闭逻辑复用现有 vault 实现。
- 两平台写入前验证 JSON；既有值的后端写入失败不会由 vault 代码主动清除或替换旧值。没有把 registry 接入登录、设备注册流程或登出协调器。

## 已执行验证

- `bash docs/evidence/P02/P02-12/verify-swift.sh` — **exit 0**。Swift 合成后端验证两个独立存储槽、登出清认证态后 registry 仍存在、非法/超限输入及写/读错误；未访问真实 Keychain。
- `bash docs/evidence/P02/P02-12/verify-kotlin.sh` — **exit 0**。Corretto 17 + Kotlin 1.9.24、Android API 35 编译生产 Kotlin；软件 AES 合成测试验证独立槽、旧认证态命名、登出隔离和失败写保留。编译有既存 `isInsideSecureHardware` 弃用警告。
- `bash docs/evidence/P02/P02-08/verify-kotlin.sh` — **exit 0**。既有 Android 认证态合成回归通过。
- `bash docs/evidence/P02/P02-07/verify-synthetic.sh` — **exit 0**。既有 iOS 认证态合成回归通过。
- `bash docs/evidence/P02/P02-12/verify-android-uts.sh` — **exit 0**。HBuilderX 5.33 生成 Android UTS Kotlin wrapper，确认 auth 与 registry 五个接口均被桥接；没有运行 dex、APK 或 Android Keystore。
- `bash docs/evidence/P02/P02-12/verify-ios-uts.sh` — **exit 1**。HBuilderX 5.33 / iPhoneOS SDK 26.5 在 framework 构建阶段被 Xcode 的 PIF 缓存缺失阻断：`Could not compute dependency graph`，报告缺失的 `WORKSPACE@v11...-json` 目录，POSIX `No such file or directory`。在隔离临时 HOME 再运行仍引用用户 Xcode DerivedData 下另一缺失 PIF 目录；没有删除或清理 DerivedData，也未继续重试。
- `git diff --check` — **exit 0**。

第一次 iOS UTS 失败现场：`/private/tmp/nerae-p02-07-uts.eyvzWXU9`（脚本前缀来自初始脚本复制时尚未改名）；隔离 HOME 的第二次失败现场：`/private/tmp/nerae-p02-12-uts.A0QOQUSC` 与 `/private/tmp/nerae-p02-12-home.2fQZZL01`。失败目录保留供诊断。

## 未验证与风险

没有在真实 iPhone Keychain 或 Android Keystore/TEE/StrongBox 上执行；没有真机/模拟器安装，也没有 APK/IPA。Android 主机 JVM 的软件 AES 只验证加解密封装，不证明硬件密钥行为。iOS Swift 源码通过本机 `swiftc` 合成测试，但此次 HBuilderX 生成的 iPhoneOS framework 未能完成。调用方集成、设备注册生命周期和跨平台兼容性仍待后续实现与真机验证。
