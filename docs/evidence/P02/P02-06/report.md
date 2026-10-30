# P02-06 · Android 硬件设备密钥与登录证明桥

## 状态

**partial，等待 Lead 安全复核。**基线 `f7b8a04bc69428143008b8419c80462690816c25`。本次运行时未提供模型名或 reasoning effort，记录为“未知”。没有安装工具、连接设备、启动模拟器或生成真实设备密钥。

## 改动

- `apps/client/src/uni_modules/nerae-device-key/utssdk/app-android/index.uts` 将三个占位错误实现替换为对 Kotlin bridge 的窄调用，并对返回 JSON、错误码、别名、公钥形状和 proof 分段进行检查。
- Kotlin 文件声明包 `uts.sdk.modules.neraeDeviceKey`，与 DCloud 的 `uni_modules` 插件默认包名规则一致，因此 UTS 入口可直接访问对象，无需 import。DCloud 官方混编文档规定包名来自插件 ID 驼峰转换，并以 `uni_modules/uni-getbatteryinfo -> uts.sdk.modules.uniGetbatteryinfo` 为例：[UTS 原生混编](https://uniapp.dcloud.net.cn/plugin/uts-plugin-hybrid.html)。
- bridge 使用 Android Keystore 的 P-256 EC 密钥。只有 `createNewDeviceKey` 生成随机别名；读取与签名仅查找既有 alias。创建后及每次读/签名前读取 KeyInfo，只接受 TEE 或 StrongBox（API 31+）；较旧 API 只接受 `isInsideSecureHardware` 为 true。软件/未知安全级别拒绝。清理标记在 `generateKeyPair()` 成功返回后才设置，避免生成失败时删除非本次创建的条目。签名前重新校验证书公钥仍是 P-256。私钥不导出，没有日志输出。
- proof 使用 `ES256`、`nerae-auth-device` audience、四种现有 purpose、resourceId/nonce、Unix 秒 `iat`、60 秒 `exp` 和 32 随机字节 JTI。`SHA256withECDSA` 输出先经过严格短格式 DER 检查，再转 32-byte `r || s`。公钥坐标为 P-256 JWK 固定宽度 `x/y`。
- `DerSmoke.java` 在进程内生成临时软件 P-256 测试密钥，反射调用生产 Kotlin 类的 `derToJose`，验证正签名转换/往返及畸形 DER 拒绝；不输出密钥或签名。`verify-der.sh` 从其位置发现仓库根与正式 Kotlin 源，支持配置 JDK、`kotlinc`、Android SDK `android.jar` 及 Kotlin stdlib 路径；成功只删除自身创建的临时目录，失败保留目录供复现。

## 验证

| 命令/检查 | 结果 | 说明 |
|---|---:|---|
| `/usr/bin/java -version` | 1 | 系统 Java 入口不可用；后续发现并使用 HBuilderX 自带 Corretto。 |
| `$HBUILDERX/plugins/amazon-corretto/bin/java -version` | 0 | OpenJDK Corretto 17.0.15。 |
| `/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc -version`（`JAVA_HOME` 指向上述 Corretto） | 0 | Kotlin/JVM compiler 1.9.24；该编译器由 Lead 提供并验证官方 SHA-256，本任务未下载/安装。 |
| `NERAE_JAVA_HOME=/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/amazon-corretto NERAE_KOTLINC=/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc NERAE_ANDROID_JAR="$HOME/Library/Android/sdk/platforms/android-35/android.jar" bash docs/evidence/P02/P02-06/verify-der.sh` | 0 | 编译正式 Kotlin 源码；JVM 合成测试中，真实 `derToJose` 将软件生成签名转为 64-byte JOSE，往返验签通过，5 种畸形 DER 和 1 个篡改签名均拒绝。仅有 `isInsideSecureHardware` 弃用警告；脚本成功后清理自身临时工作目录。 |
| `command -v gradle`、`command -v kotlin` | 未找到 | 没有 Gradle 构建器；本次 Kotlin 源编译直接使用独立 compiler。 |
| `~/Library/Android/sdk/cmdline-tools/latest/bin/sdkmanager --version` | 1（未设置 `JAVA_HOME`） | 提示无法定位 Java Runtime；后续找到 HBuilderX JDK，但本任务不安装 SDK 组件。 |
| `test -d ~/Library/Android/sdk/build-tools` | 失败 | SDK 缺少 Build Tools。 |
| `test -x apps/client/node_modules/.bin/uni` | 失败 | 当前 checkout 未安装项目依赖；本任务未安装依赖。 |
| `GOCACHE=/private/tmp/nerae-p02-06-go-cache GOPROXY=off GOSUMDB=off /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test ./internal/modules/auth/domain`（工作目录 `apps/api`） | 0 | 只验证既有 Go proof verifier 的合成测试；没有运行或生成 Android proof。 |
| `git diff --check` | 0 | 无空白错误。 |

`verify-der.sh` 通过 `bash` 调用；尝试设为可执行文件模式时，当前受限文件系统拒绝 `chmod`（Operation not permitted），因此不以直接执行脚本的形式运行。

本机只读盘点：Android Platform 35、`android.jar`（26 MiB）、`adb` 37.0.0 和一个未启动的 AVD 配置存在；未找到 Android Studio、Gradle 或 SDK Build Tools。先前只检查了系统 Java 入口，漏掉 HBuilderX Corretto 与 Lead 提供的独立 Kotlin compiler；本轮已用它们编译实际 Kotlin 文件。没有启动 AVD。没有 Android 真机，硬件级别检测、持久化/重启恢复、TEE/StrongBox 真实表现及 Keystore 签名未验证。

## 安全与限制

- Go 验证器只验证密钥持有证明，不验证 Android Key Attestation；客户端 KeyInfo 结果不能向服务端证明硬件来源。当前范围不宣称服务端强制硬件密钥。
- Android 的 TEE 与 StrongBox 不等同于 iOS Secure Enclave；本实现按任务卡接受两类硬件级别。旧系统的硬件检测精度较低，仍须真机审核。
- Android 别名与账号主体的映射、登录 UI/HTTP、服务端注册及撤权接线不在本模块中。
- Kotlin/JVM 源码已由独立 Kotlin 1.9.24 编译，但未运行 HBuilderX/uni-app UTS 混编流程；UTS 同包对象解析、应用构建和具体设备 Provider 行为仍待验证。DER 测试使用临时软件生成的合成 P-256 密钥，未调用 Android Keystore，也未生成或保存真实设备密钥；Go 测试不能替代这些验证。

## 证据摘要

文件 SHA-256：

- `apps/client/src/uni_modules/nerae-device-key/utssdk/app-android/index.uts`: `1e1189184f9f0522f21225a86341f7372f81656b1345e36497db966f80e98f56`
- `apps/client/src/uni_modules/nerae-device-key/utssdk/app-android/NeraeAndroidDeviceKeyBridge.kt`: `8994be255162796665e2f115e18a5245a803f24261f6089a807a251817d6db27`
- `docs/evidence/P02/P02-06/DerSmoke.java`: `788a1db58e30e127bb06c0766776f01e5ba9525bd2ab4a1b4765108776b53384`
- `docs/evidence/P02/P02-06/verify-der.sh`: `0504ab89262a0ac192bfacfce02f3dd60e79df47ba038f6444376e63f6779998`

没有 Android APK、UTS 构建产物或真机证据；JVM 编译 class 位于 `/private/tmp/nerae-p02-06-kotlin-out`。本卡在 Android UTS/APK 编译、硬件密钥和登录接线未验证前保持 partial/active。

## Lead 复核

审查时发现 Kotlin 混编包名缺失：按 DCloud 官方规则补为 `uts.sdk.modules.neraeDeviceKey`；同时补齐签名前的 P-256 证书校验，并避免生成抛错时清理不确定归属的别名。修改后由 Lead 独立执行 `NERAE_JAVA_HOME=... NERAE_KOTLINC=... bash docs/evidence/P02/P02-06/verify-der.sh`，退出 0；编译仍有旧系统兼容 API 的弃用提示。HBuilderX UTS 同包解析、目标设备安全硬件及私钥生命周期尚未运行，故此复核仅支持保存阶段性实现，不作为可用 Android 登录的接受结论。
