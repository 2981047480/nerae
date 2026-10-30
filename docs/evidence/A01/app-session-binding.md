# App 会话安全存储接线 · 2026-10-31

App 平台会话工厂通过 UTS 插件根目录绑定系统安全存储，并把调用方提供的 `/me` 端口接到已有服务端会话验证器。一个 App JS 进程只创建一个协调器，重复使用相同端口返回同一实例；尝试换用另一个端口会失败。工厂本身不选择服务器，产品启动前仍需由可信、固定的服务配置创建该端口，不能把用户输入的任意地址用于发送凭据。

在集成版本执行：

- `pnpm --filter @nerae/client run typecheck`：退出 0。
- `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test src/platform/app/nativeSession.test.mjs src/features/auth/sessionCoordinator.test.mjs src/features/auth/meVerifier.test.mjs src/platform/app/nativeHttpsTransport.test.mjs`（在 `apps/client`）：38 项通过。新测试替换 UTS 入口为合成内存实现，检查单实例、恢复时服务端验证、临时失败保留凭据及退出清理；不代表真实 Keychain/Keystore 执行。
- `python3 scripts/quality/verify_uts_plugin_build.py`：退出 0。5.26 夹具从实际 App 会话工厂与 HTTPS 工厂导入，并检查 iOS/Android wrapper、JS 代理及小程序回退；它不运行插件。
- 已预留设备密钥 alias 的集成源码，Swift 和 Android Kotlin/API 35 canonical alias 合成检查通过；Android 编译只有既有废弃 API 警告。未创建真实设备密钥。

尚未接到 App 入口或登录页面；无可信生产服务器地址、真实发信和双设备验证。5.26 生成原生源码不代表原生 framework 链接、签名安装或手机运行成功。
