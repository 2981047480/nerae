# P02-08 Android 安全状态存储桥验证

## 状态

**Partial。** 已实现 Android UTS 到 Kotlin 的认证状态 vault 桥，并通过本机 Kotlin 编译、软件 AES-GCM 合成测试和 HBuilderX 5.33 UTS 源码转换。没有在 Android 设备或模拟器上运行，也没有生成、签名、安装 APK；因此 Android Keystore、TEE/StrongBox、锁屏限制和真实文件原子替换仍未验证。

## 实现范围

- `apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/NeraeAndroidSecureVaultBridge.kt`
- `apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/index.uts`
- `docs/evidence/P02/P02-08/VaultSyntheticSmoke.kt`
- `docs/evidence/P02/P02-08/verify-kotlin.sh`
- `docs/evidence/P02/P02-08/verify-uts-source.sh`

Kotlin 实现将有版本的 AES-GCM envelope 存入 app 私有 `noBackupFilesDir` 的 `AtomicFile`，独立 AES 密钥使用专用 Android Keystore alias。Android P+ 优先请求 StrongBox；只有 StrongBox 不可用时才尝试默认硬件 provider，并检查硬件安全级别；无法确认硬件保护时失败关闭。旧密文存在但密钥缺失或数据无法解密时，不创建替代密钥、不清除旧密文。输入限为有界 UTF-8 JSON 对象，由非 lenient `JsonReader` 完整消费一个对象并要求文档结束，嵌套深度最多 64；恢复文件检查位于流的 `use` 作用域内，确保检查失败也关闭已打开的流。原生异常仅转换为固定错误码。

首次写入在密文和别名均不存在时才生成密钥。读到无密文时也会检查专用 alias：若 alias 孤立或检查异常则返回 unavailable，不报告 found=false。若首次写入中断而遗留孤立密钥，后续写入同样失败关闭。替换依赖 Android `AtomicFile`，失败时只返回 unavailable；本验证不承诺或实测断电场景下旧值必然保留。`clear` 仅删除 vault 的精确 base 文件及 AtomicFile 使用的 `.new` / `.bak` 恢复文件，并逐一确认它们消失；随后删除 vault 专用 alias，不查询或删除设备签名密钥。

AtomicFile 路径核对依据 [AOSP `android.util.AtomicFile`](https://android.googlesource.com/platform/frameworks/base/%2B/HEAD/core/java/android/util/AtomicFile.java)：当前实现使用 `<base>.new` 与旧实现恢复用 `<base>.bak`；`openRead()` 会先恢复 `.bak`，base 存在时处理 `.new`；`delete()` 删除 base、`.new` 和 `.bak`。代码只触及这三个精确文件名，不扫描或清理目录。该核对来自源码，未在 Android runtime 验证文件系统行为。

## 实际验证

在工作树根运行：

```sh
bash docs/evidence/P02/P02-08/verify-kotlin.sh && bash docs/evidence/P02/P02-08/verify-uts-source.sh
```

**退出码：0。** 实际工具为本机 Corretto Java 17.0.15、Kotlin 1.9.24、Android API 35 `android.jar`、HBuilderX 5.33 自带 UTS Kotlin 转换器。Kotlin 源编译成功；合成测试覆盖软件 AES-GCM 封装、损坏/截断密文、缺失与孤立密钥、读失败不删除孤立 key、清除存储失败时保留 key、JSON 深度 64/65 边界、错误分类、JSON 输入大小边界、原子存储替换失败路径、清除及重写。HBuilderX 生成了 UTS wrapper 与 Kotlin bridge，并确认 wrapper 调用了读、写、清除三个入口。Android `JsonReader` 的运行时严格解析行为没有在设备上执行；此处只编译生产 Kotlin 源。

合成测试注入的是软件 AES 密钥和内存存储，仅验证封装逻辑；它不验证 Android Keystore 或 Android 文件系统行为。UTS 转换只生成 Kotlin 源码，没有 DEX、APK 或 Keystore 测试。

过程中的首次完整验证因脚本在设置 `JAVA_HOME` 前检查 Kotlin 版本而退出 **2**（`Expected Kotlin 1.9.24`）；修正后先前运行退出 **0**。本次改动后一轮编译因临时移除的 `JSONObject` import 仍被 bridge 响应编码使用而退出 **1**，恢复 import 后 Kotlin 编译及合成测试退出 **0**；随后 UTS 转换与 `git diff --check` 均退出 **0**。

UTS 生成结果另以 `NERAE_KEEP_P02_08_UTS=1` 重跑保留在本机临时目录，人工确认输出包含 `app-android/src/index.kt` 和 `app-android/src/NeraeAndroidSecureVaultBridge.kt`；该探针退出 **0**。临时目录为 `/private/tmp/nerae-p02-08-uts.4UUdcTHl`，仅供复核，不是交付产物。

最后执行 `git diff --check -- apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android docs/evidence/P02/P02-08`，退出 **0**。此任务文件仍为未提交状态。

## 未验证与风险

- 没有 Android 设备或模拟器验证 Keystore alias 生命周期、硬件级别检测、StrongBox 到 TEE 回退、凭据可用性、锁屏/用户解锁限制及 `setUnlockedDeviceRequired` 行为。
- 没有验证 `AtomicFile` 在进程终止、系统崩溃、备份文件恢复及存储权限异常时的运行行为；失败返回 unavailable，不代表已证明旧值保留。
- 没有完成 Android Gradle/DCloud 原生链接、DEX/APK 构建、签名、安装或客户端真实调用链验证；UTS 转换使用 HBuilderX 5.33，不能证明项目锁定工具链上的完整原生构建。
- 代码无法仅凭本机 `KeyInfo` 检查向服务端证明硬件密钥属性；没有实现远程硬件证明，也没有测试生产认证状态生命周期。
- Kotlin 编译有 Android API 35 上旧 API 分支 `KeyInfo.isInsideSecureHardware` 的弃用警告；该分支用于 API 30 及以下的硬件检测。

## 文件 SHA-256

```text
```

本次复核后的文件 SHA-256：

```text
40c4b14a130c83dbf973405785a3677913c10b00aa13c36ca5bf36050c634eb1  apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/NeraeAndroidSecureVaultBridge.kt
39aabfc0536b5d59f829397ed72bfdae7c460bb05a11201f3d790a7eeae08668  apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/index.uts
e600943ff5d9df06e0e4e76431d9d7ccac40193b1a55cd22726796453609922f  docs/evidence/P02/P02-08/VaultSyntheticSmoke.kt
73b94d817d83f05b15d90cbc69140b853cfd49891fbc9e1254ea7a23d48b1846  docs/evidence/P02/P02-08/verify-kotlin.sh
9204a2dfa8e86410a9303b854dfae2b0b1b10e5d19a69b51127469386d9c9bcd  docs/evidence/P02/P02-08/verify-uts-source.sh
```
