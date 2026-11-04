# 训练待发日志：首次发送保护接口

`TrainingOperationJournal.beginSend` 现在只通过 `claimFirstSend` 签发许可。该接口要求存储在同一原子操作中记录不可回退的逐操作发送标记并完成 ready → in-flight；普通字节 CAS 或单独的槽版本不满足此要求。缺少该接口或原生调用结果不确定时，保留原始请求，不签发许可。

`SecureOperationJournalStorage` 增加适配入口，并要求原生端重新校验当前 registry、所有者/事件/请求身份、唯一目标项变化与原子标记。迁移前历史无法证明时必须失败关闭。**iOS、Android 和小程序当前均没有实现这个原生能力，真实存储的发送入口因此不可用；这是防止重复发送的保护，不是完整同步实现。**

本次测试中的原生端是内存合成实现，用于检查适配契约，包括旧 CAS 回写 ready、删除后重建、并发获取许可和提交后响应异常；它不能证明真实 Keychain/Keystore 原子性、进程外写入、设备备份回滚或跨设备唯一发送。既有 ABA 复现脚本记录的是旧基线，在本次门禁下会在首次发送处被拒绝；不能把这一拒绝视作原生 tombstone 已实现。

## 验证

- `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/*.test.mjs`：训练相关合成测试通过。初次全量运行有 7 个回执测试因其内存夹具缺少新接口失败；补齐夹具后复测通过。
- `pnpm --dir apps/client typecheck`：退出 0。
- `pnpm --dir apps/client build:app` 和 `pnpm --dir apps/client build:mp-weixin`：源码资源构建退出 0，不是安装包或真机验证。

后续仍需在 iOS/Android 各自实现并验证原生持久标记、旧数据迁移和失败恢复；小程序需单独确定可证明的原子存储边界。还需要可靠回执结算、草稿恢复、页面接线以及真实跨设备联测。

后续 iOS 候选已实现首次发送标记并接入 UTS 与存储工厂，见 [iOS 桥接证据](../../P02/P02-13/ios-dispatch-bridge.md)。此进展未完成回执结算、真实发送编排或 Android／小程序支持。
