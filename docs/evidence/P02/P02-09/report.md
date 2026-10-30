# P02-09 三端安全随机请求键基础

## 状态

**Partial。** 已提供 iOS、Android 同步 UTS 接口和微信小程序异步适配器；本机原生源码、合成编码/错误测试及锁定 uni-app 小程序资源构建通过。没有微信账号或三端真机，未接入业务登录、幂等请求或真实待发队列。本地测试不能证明系统随机源在目标设备上的熵或平台运行行为。

## 实现

- `apps/client/src/uni_modules/nerae-random/`：UTS 结果接口、iOS/Android 原生随机桥和平台 fallback。iOS 每次调用 `SecRandomCopyBytes` 填充 32 字节；Android 使用 `java.security.SecureRandom` 填充 32 字节。两者均输出无填充 base64url，原生失败返回空值，由 UTS 层捕获异常并以固定错误码失败；UTS 校验 43 位 base64url 格式。没有非密码学随机、时间或设备标识回退。
- `apps/client/src/platform/weixin/secureRandom.ts`：调用 `wx.getRandomValues({ length: 32 })` 的 Promise 适配器。只接受恰好 32 字节的 `ArrayBuffer`，单次 settle，缺接口、回调失败、同步异常或格式错误统一拒绝，不读取或打印底层错误。测试注入的 API 仅供合成用例；生产入口直接使用全局 `wx`。
- 微信类型声明来源为本机已锁定 `@dcloudio/types 3.4.31`：`uni-app/uni-patches/wx/index.d.ts` 声明 `getRandomValues` 生成密码学安全随机数、基础库 **2.15.0** 起支持，且插件中不支持。适配器不预判客户端版本；运行时接口缺失时失败关闭。

## 验证

验证依赖来自现有集成工作树已安装的本地依赖目录，仅通过临时目录 symlink 使用，没有安装或下载依赖。目标工作树本身没有 `node_modules`。

1. `bash docs/evidence/P02/P02-09/verify-ios.sh`：退出 **0**。HBuilderX **5.33.2026102401**、Node **26.0.0**、iPhoneOS SDK **26.5** 将 UTS 模块编译为未签名 iPhoneOS arm64 framework，原独立工作树编译的 framework SHA-256 为 `6f8a02ea823b970ff84ee62bf6a3fa02c070ca20b899c8187309f2e484962f37`。同脚本还在 macOS Swift 主机执行固定 32 字节编码向量和系统随机输出格式检查；随机值不输出。此产物不是 IPA。
2. `bash docs/evidence/P02/P02-09/verify-android.sh`：退出 **0**。HBuilderX 5.33 转换 UTS 生成两个 Kotlin 源文件并检查 wrapper 调用原生桥；Corretto Java **17.0.15**、Kotlin **1.9.24**、Android API **35** 编译实际 Kotlin 桥。JVM 合成探针使用宿主 `SecureRandom`，验证零向量、`00..1f` 编码向量与 43 位输出格式，不输出随机值。它不验证 Android 的 SecureRandom provider 或 Android 设备。
3. `bash docs/evidence/P02/P02-09/verify-weixin.sh`：退出 **0**。TypeScript 格式检查只针对本次新增 adapter；完整 client typecheck 通过；6 个注入 API 测试通过，覆盖精确编码、缺失接口、API 失败/同步异常、异步回调处理异常、非 ArrayBuffer、31/33 长度及重复回调。随后调用现有本机 uni CLI（未经 pnpm）在临时客户端副本执行 `build -p mp-weixin`；日志显示编译器 **5.26（vue3）**，生成非空 `app.js`（374 字节）和含 `getRandomValues` 的可达 adapter 资源（918 字节）。测试向量只验证编码，不是随机熵证明。
4. `git diff --check -- apps/client/src/uni_modules/nerae-random apps/client/src/platform/weixin docs/evidence/P02/P02-09`：退出 **0**。

首次 WeChat 格式检查扫描了整份临时源码，发现既有 `recordingApi.ts` 和 `recordingProjection.ts` 分别存在 8/26 处格式差异；验证范围调整为本次 adapter 单文件格式检查，之后完整 client typecheck 及该文件格式检查通过。第一次直接调用 pnpm 构建时，它尝试启动 `pnpm install` 并访问 registry（请求报 EPERM）；已 Ctrl-C，退出 **130**，没有完成依赖安装；改用已有本地 `uni` CLI 后构建成功。第一次 iOS framework 编译暴露 UTS 对象字面量映射错误，改为项目既有 `JSON.parseObject` 类型映射后复跑通过。受限沙箱的第一次 Xcode 调用遇到共享 PIF 缓存访问失败；在正常获准的本机 Xcode 构建环境复跑通过。

## 边界与风险

- 没有微信基础库 2.15.0 以下/以上真机、微信开发者工具真机预览或账号验证；类型声明与资源构建不能证明实际微信运行时 CSPRNG 可用。
- iOS framework 是未签名的 arm64 framework，Swift 主机 smoke 不是 iPhone 真机；未生成 IPA、未签名、未安装。
- Android Kotlin 编译和 JVM `SecureRandom` smoke 不是 Android runtime、真机或安装包验证。
- 无业务调用；同一请求重试时如何保留同一个 request key 仍由调用方设计，本卡没有实现持久待发队列或 HTTP 幂等接线。
- 固定测试输入仅覆盖 base64url 编码；测试未保存真实随机值，但任何格式测试都不证明熵质量。

## SHA-256

```text
630158b04e3b45cf9a6bfa38496ba39bf970ec59e09fc5e5de34c1a714bb6050  apps/client/src/uni_modules/nerae-random/package.json
b57f2d98b0e74160b3291c9740fbd75284e40eaada6820a97383de5db616b492  apps/client/src/uni_modules/nerae-random/utssdk/interface.uts
3adc73b585df502f3c4dc09f1b87a0ff33944ad37df312696250279301c389d6  apps/client/src/uni_modules/nerae-random/utssdk/index.uts
1c7895a9db5903ce8f5e9b5f5a4ed8a5d90ad6b5c3c1abce9857be5059b8aa47  apps/client/src/uni_modules/nerae-random/utssdk/app-ios/NeraeSecureRandomBridge.swift
0ebe2e2eb6ea363d7df922018a6cd3a7c36e39764f98adaf0c6fd6ce25b034a6  apps/client/src/uni_modules/nerae-random/utssdk/app-ios/index.uts
c8361c7fceae787be49f640508fc2314d9ad67fa6a687b1a18deb3600b5ca808  apps/client/src/uni_modules/nerae-random/utssdk/app-android/NeraeAndroidSecureRandomBridge.kt
03f210a8facd0d870d2ddf7af19a7a8d80fe16c20ed083a430c145640fd2faa7  apps/client/src/uni_modules/nerae-random/utssdk/app-android/index.uts
290a7c194a296148f096956819b624bf7b582ca57768dadee04468bccdfae1ca  apps/client/src/platform/weixin/secureRandom.ts
1577487632c5706d8e76d559750629dab3425d5831cd829fb393824b18b06c02  docs/evidence/P02/P02-09/android-random-smoke.kt
f524ae289e9995ddd25b4210745022e298fbd166d046e8e63cb8cf1efa88c6c6  docs/evidence/P02/P02-09/ios-random-smoke.swift
473258ce894e1961a4428cdf36301fda4bb8adf2cedc4853fa9fc17c7c63a9df  docs/evidence/P02/P02-09/weixin-random.test.mjs
f81799e9d042c5e927870bd03b4e1a64adb192976e5470d40a8cc5898755b70f  docs/evidence/P02/P02-09/verify-ios.sh
43443b8c44542b065fe13c77150b20c6d280406784ed0164abc934e57a65509d  docs/evidence/P02/P02-09/verify-android.sh
096b06f6a1aed52c96e393fd0da53cd2631f0b4f5cf2681403577d2de8d5faf3  docs/evidence/P02/P02-09/verify-weixin.sh
```

独立集成复核发现微信随机回调内部若抛异常可能使 Promise 悬挂；已在回调内关闭并返回固定错误，补充异步异常回归。集成工作树的微信测试和资源构建重新通过。

集成工作树独立复跑 `verify-ios.sh` 和 `verify-android.sh` 均退出 0；本次生成的 iPhoneOS framework SHA-256 为 `a2f56adac09900d43860cef05320618db561a96e240834d70189dcaf12d0540c`，不同构建产物哈希不作为可重复构建证据。
