# iOS 训练待发首次发送候选

2026-11-03，`NeraeVersionedTrainingJournal.claimFirstSend` 在单进程锁内将「ready → in-flight」、活动训练标记和请求历史标记写入同一个 Keychain 项。普通版本化 CAS、旧 CAS、逻辑删除及重建都保留原生元数据；未知写入结果不签发许可。v1/v2 数据没有可靠发送历史，保留其内容并禁止首次发送。

请求历史分别绑定主体/路径/重试键以及主体/路径/规范化正文；独立活动标记绑定训练事件，阻止旧 CAS 通过更改路径、请求键或正文绕过。同一训练的下一请求须等待可信服务端回执结算；当前没有结算入口，因此候选不会清理活动标记。

验证：`bash docs/evidence/P02/P02-13/verify-ios.sh` 在 Mac 编译 Swift 并运行注入式存储测试，覆盖旧 CAS 回退、删除重建、改键/路径、正文重排、旧数据迁移、同进程竞争、写入结果不明以及历史 8192 项和活动 64 项上限。上限耗尽时保留原数据并失败关闭。该测试不是 iPhone Keychain、设备重启或跨进程测试。

补充编译检查：本机 Xcode 26.5 的 iPhoneOS SDK 将该 Swift 文件编译为 arm64 目标文件，`vtool` 显示 `platform IOS`；这不是 framework 或 App。原 `verify-ios-uts.sh` 因其预期的 `/private/tmp/nerae-hbuilder-download/HBuilderX.app` 不存在而退出 2，本轮没有完成 UTS/iOS framework 编译。

限制：方法尚未暴露给 UTS 或应用，实际三端发送门禁不变。活动标记不会自行结算，直接接线将阻断同一训练的后续保存组和完成操作；不能将本候选当作可用的跨设备同步。后续需由原生可信传输或服务端签名回执验证结果并原子结算，同时保留请求历史；还须校验当前登录主体、验证真实 Keychain 和进程范围，并为历史容量提供安全维护策略。

后续进展：首次发送标记已接入 iOS UTS 和 App 存储工厂，见 [桥接证据](ios-dispatch-bridge.md)；可信回执结算和实际发送编排仍未完成。
