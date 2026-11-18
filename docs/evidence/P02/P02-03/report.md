# P02-03 Android 安装链路进度

- 2026-11-18：没有单独推进本卡。与 P02-02 同一次 `pnpm --filter @nerae/client build:app` 在 App 资源里生成了 13 个 Kotlin 源文件；详见 [P02-02 资源编译](../P02-02/report.md)。本环境没有 Android SDK、`sdkmanager`、Gradle、`adb` 或 Android 设备，没有 DCloud Android 离线 SDK，没有产出或签名 APK。真机未跑。本卡仍未接受。
- 2026-11-15：对本轮草稿版本及组恢复改动，使用 Kotlin 1.9.24、Corretto 17 和 Android API 35 复跑 `P02-13/verify-draft-android.sh`，合成存储回归退出 0；使用本轮 5.33 App 资源生成的 Android `index.kt` 运行 `P02-13/verify-generated-draft-versioned.sh`，桥接签名检查退出 0。另以独立 JVM 合成用例验证版本 1→2、旧版本降写拒绝、重复 schema 拒绝和待发标记保留。Android 框架 JSON、DCloud 运行时链接、设备 Keystore、APK 与真机仍未验证。
- 状态：partial；Project Lead，`gpt-6.1-terra`，推理等级未由运行时报告。代码基线是 `a8cd57fd69261871d59c8281f3e5c9ef73428c4b` 加本轮未提交的登录页改动，尚无接受提交。
- 为复验现有原生模块，在本轮临时目录恢复 Corretto 17 与 Kotlin 1.9.24，分别核对了官方提供的 SHA-256；系统 Android SDK 使用本机已有 API 35。没有改系统工具链或项目依赖锁。
- 使用 `NERAE_JAVA_HOME`、`NERAE_KOTLINC` 和 `NERAE_ANDROID_JAR` 指向上述工具，运行 `bash docs/evidence/P02/P02-18/verify-android-dispatch.sh`：退出 0，14 组合成解析用例及生产日志、活动请求 CAS 和桥接合成场景通过。JVM JSON 使用测试替身，未运行设备 Keystore。
- 使用同一工具，运行 `bash docs/evidence/P02/P02-18/verify-generated-dispatch.sh <5.33 App 构建产生的 Android index.kt>`：退出 0，生成的请求转发调用可与生产 Kotlin 桥一起类型检查；UTS 类型使用测试替身，未链接 DCloud 运行时。两次编译均报告 Android API 弃用警告；第二次额外报告测试替身未使用的参数。
- 官方 [Android 离线 SDK 版本记录](https://nativesupport.dcloud.net.cn/AppDocs/download/historyRelease/androidRelease)列出 5.33 的下载记录；本轮尚未取得并验证 Android SDK，也没有构建 APK、签名、安装或运行。本机 App/小程序资源构建和上述合成检查不能替代这些步骤。用户目前没有 Android 真机，本卡仍为 active。
