# U02-01 持久待发请求日志基础

日期：2026-10-31

基线：`a58a4a3cf1bf173bf4a14f0876f3968ca006a30e`

## 结果

新增 `TrainingOperationJournal`，为同一已认证主体和 `clientEventId` 保留至多一笔未决 POST。日志精确保留 method、path、JSON body 和 `Idempotency-Key`；`stage()` 必须先经持久存储的原子 `createIfAbsent` 成功，`beginSend()` 再经原子 CAS 持久标记为 `in_flight` 后，才返回不含 Authorization/token 的发送许可。网络结果未知时调用方只能标记 `uncertain`；同一键和同一请求可恢复，改键、改路径或改正文被拒绝。

每个 journal 实例绑定构造时的 subject 与 generation。旧实例在切号/重登后不能读或发；同一主体重新登录须创建绑定新 generation 的实例，才能读取并重试原记录。回执不能以 raw HTTP status/body 结算。显式 `reconcile()` 要求调用方检查当前操作；若报告已应用，还必须提供当前账号/会话下由 `RecordingApi` 签发、事件 ID 与资源 ID 匹配的自由训练快照。它只清待发日志并返回快照，不更新草稿的已保存状态；调用方还需验证该快照确实对应原请求，再合并进 `DraftController` 并持久化。无法确定时记录保持待核对；确认未应用只允许原键原文重试。

## 验证

- `node --test apps/client/src/features/training/operationJournal.test.mjs`：12 项通过。
- `node --test apps/client/src/features/training/*.test.mjs`：106/106 通过。
- `node --test apps/client/src/features/auth/*.test.mjs apps/client/src/features/training/*.test.mjs`：训练用例通过；auth 的 `meVerifier.test.mjs` 与 `sessionCoordinator.test.mjs` 在 Node 26 下因既有 `sessionCoordinator.ts` 的 extensionless `emailAuthApi` import 无法解析而失败。这不是 journal 测试失败。
- `pnpm --filter @nerae/client typecheck`：未完成。pnpm 先尝试依赖安装，临时文件 `_tmp_41511_fb712677a4602578719b8715db0c9755` 创建时报 `EPERM` 并以 255 退出；当前工作树没有可用的本地 `vue-tsc`。未安装依赖、未修改锁文件。

## 接入前置与限制

- 当前实现只定义 `JournalStorage` 端口，没有平台存储实现。真实适配器必须提供受保护的持久化、原子 create/CAS/delete，校验落盘完整性，并避免普通明文存储训练正文。内存测试存储只验证模块控制流，不证明平台持久性/加密/原子性。
- `retentionMs` 必须由调用方显式设置，且短于服务端幂等回执保留期。到期只阻止自动重试；日志不会自动删除，仍可用于读取/核对。客户端时钟只用于本机重试限制，不是服务端过期证明。
- 当前 `RecordingApi` 没有接受已存 raw request 的重放入口；它会从对象重新序列化请求。跨重启时的 `ScopedSessionDetail` WeakMap 凭据也不会持久化。因此 journal 尚未接线到网络发送。后续要么由受认证原生 transport 发送精确 body，并经 `RecordingApi` 等价严格验证器核验；要么增加窄 replay seam，避免绕过认证、schema 和账号校验。
- journal 不生成 `clientEventId` 或幂等键；调用方须用 CSPRNG 提供稳定键。日志包含训练请求正文，虽不包含认证凭据，仍需明确的 at-rest 保护和保留/删除策略。
- 任意 `reconcile` 回调都是应用层信任边界：对于 sets/finish 必须用新鲜的同主体服务端快照与原 body、expectedVersion 比较；单有合法 `RecordingApi` 品牌、HTTP 200 或同资源 ID 不足以证明该写入成功。实际应用页面、真实存储、网络、HTTP/DB 和跨设备行为均未验证。
