# U02-01 持久待发请求日志基础

日期：2026-10-31

基线：`a58a4a3cf1bf173bf4a14f0876f3968ca006a30e`

## 结果

`TrainingOperationJournal` 为同一已认证主体和 `clientEventId` 保留至多一笔未决 POST。日志精确保留 method、path、JSON body 和 `Idempotency-Key`；`stage()` 必须先经持久存储的原子 `createIfAbsent` 成功，`beginSend()` 再经原子 CAS 持久标记为 `in_flight` 后，才返回不含 Authorization/token 的发送许可。网络结果未知时调用方只能标记 `uncertain`。本修订仅允许新 staged、`ready` 且 revision 为 1 的请求取得首次发送许可；已持久化为 `in_flight` 或 `uncertain` 的操作一律拒绝再次发送，并保留原 key/body。模块不删除或改写独立草稿，但因未接入 draft controller，草稿保留接缝仍未验证。改键、改路径或改正文被拒绝。

每个 journal 实例绑定构造时的 subject 与 generation。旧实例在切号/重登后不能读或发；同一主体重新登录可读取原记录，但新 generation 也不能重发 `in_flight`/`uncertain`。客户端时间或假设的幂等 TTL 不作为重放授权。`reconcile()` 不接受调用方裸 `not_applied` verdict；`applied`/`unknown` 也不会结算请求。当前接口不能证明快照或调用方判断对应已存的请求体、键和版本，因此所有不确定操作保持待核对。模块当前没有恢复重发或清除待核对日志的路径，必须等服务端提供请求绑定证据的回放接缝后另行实现。

## 验证

- 基线上的既有 Node 与类型检查结果保留为历史记录；本 follow-up 的当前结果见下节。
- `node --test apps/client/src/features/auth/*.test.mjs apps/client/src/features/training/*.test.mjs`：训练用例通过；auth 的 `meVerifier.test.mjs` 与 `sessionCoordinator.test.mjs` 在 Node 26 下因既有 `sessionCoordinator.ts` 的 extensionless `emailAuthApi` import 无法解析而失败。这不是 journal 测试失败。
- `pnpm --filter @nerae/client typecheck`：未完成。pnpm 先尝试依赖安装，临时文件 `_tmp_41511_fb712677a4602578719b8715db0c9755` 创建时报 `EPERM` 并以 255 退出；当前工作树没有可用的本地 `vue-tsc`。未安装依赖、未修改锁文件。
- TypeScript 配置为 `moduleResolution: Bundler` 且没有 `allowImportingTsExtensions`；journal 源码不使用运行时 `.ts` 扩展导入。测试文件通过 `.ts` 导入源码，和既有 Node 测试约定一致。静态类型检查仍待依赖可用后确认。

## 接入前置与限制

- 当前实现只定义 `JournalStorage` 端口，没有平台存储实现。真实适配器必须提供受保护的持久化、原子 create/CAS/delete，校验落盘完整性，并避免普通明文存储训练正文。内存测试存储只验证模块控制流，不证明平台持久性/加密/原子性。
- `retentionMs` 必须由调用方显式设置。时间仅对从未发送的 `ready/revision=1` 首发作本地保留限制；任何不确定状态都不因时钟、时钟回拨或估计的服务端幂等保留期变成可重发。日志不会自动删除，仍可读取/核对。没有服务端请求绑定证据时，不承诺自动恢复发送。
- 当前 `RecordingApi` 没有接受已存 raw request 的重放入口；它会从对象重新序列化请求。跨重启时的 `ScopedSessionDetail` WeakMap 凭据也不会持久化。因此 journal 尚未接线到网络发送。后续要么由受认证原生 transport 发送精确 body，并经 `RecordingApi` 等价严格验证器核验；要么增加窄 replay seam，避免绕过认证、schema 和账号校验。
- journal 不生成 `clientEventId` 或幂等键；调用方须用 CSPRNG 提供稳定键。日志包含训练请求正文，虽不包含认证凭据，仍需明确的 at-rest 保护和保留/删除策略。
- 任意 `reconcile` 回调都是应用层信任边界：对于 sets/finish 必须用新鲜的同主体服务端快照与原 body、expectedVersion 比较；但当前 journal 不会据此清除已应用操作，因为现有 `RecordingApi` 没有可跨重启验证的请求绑定回执。单有合法 `RecordingApi` 品牌、HTTP 200 或同资源 ID 不足以证明该写入成功。实际应用页面、真实存储、网络、HTTP/DB 和跨设备行为均未验证。

## Fail-closed recovery follow-up

以 `2d405b41a615440bf948536fc6fec4fe564fff58` 为基线。`beginSend()` 现在只允许 `ready/revision=1` 的首次发送，并继续要求 `stage()` 已持久化、发送前标记 CAS 成功、permit 仍属于原 subject/generation。任何 `in_flight`、`uncertain` 或 revision 已推进的旧操作均返回 `reconciliation_required`，不读取本机时钟来授权重放；同一请求的 key/body 与草稿保留。`reconcile()` 不再接受 `not_applied`，应用层的 `applied`/`unknown` 也不能把记录恢复成可发送状态。未来必须由服务端提供绑定原请求的证据后，另行实现恢复和结算。

验证（本轮实际执行）：

```sh
node --import /Users/zephyr/.codex/worktrees/nerae-device-proof/nerae/docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/operationJournal.test.mjs
node --import /Users/zephyr/.codex/worktrees/nerae-device-proof/nerae/docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/*.test.mjs
apps/client/node_modules/.bin/vue-tsc --noEmit -p apps/client/tsconfig.json
```

结果：journal 定向测试 16/16、训练 feature 测试 110/110、`vue-tsc` 退出码 0。类型检查仅在临时 clone 中通过指向现有 `nerae-client-recording` 离线依赖目录的临时 symlink 执行；未安装依赖或改锁文件。`git diff --check` 通过。当前基线不含 `SecureOperationJournalStorage`，因此本轮测试使用合成 CAS 存储端口；没有声称真实原生适配器、vault、网络或服务端已验证。

限制：不确定请求现阶段无法自动重发或清除；用户须保留草稿并等待后续请求绑定证据接缝。跨进程崩溃后同样失败关闭。没有页面、生产路由、平台存储或 HTTP/DB 接线。

## Request-field allowlist follow-up

`normalizeRequest` now validates the JSON body against a conservative path-specific field allowlist before writing to any injected `JournalStorage`: free/planned session create, set batch with nested set-entry allowlist, and finish. Unknown top-level or nested fields are rejected, including `deviceProof`, `approvalSecret`, `accessToken`, and the read-only `resolvedExerciseName`; the guard is not relying on a credential-key substring denylist. Legal current fields for all three routes are covered by direct `TrainingOperationJournal.stage()` tests over a generic in-memory `JournalStorage`.

Verification after this follow-up:

```sh
node --import /Users/zephyr/.codex/worktrees/nerae-device-proof/nerae/docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/operationJournal.test.mjs
node --import /Users/zephyr/.codex/worktrees/nerae-device-proof/nerae/docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/*.test.mjs
apps/client/node_modules/.bin/vue-tsc --noEmit -p apps/client/tsconfig.json
```

Results: journal tests 17/17 and training feature tests 111/111 passed; `vue-tsc` exit 0 using the same temporary symlink to existing offline dependencies described above; no dependency installation or lockfile edit. `git diff --check` passed. The field allowlist mirrors wire object shapes but does not replace full OpenAPI or server/domain validation. This base does not include the later secure native storage adapter; tests verify the journal's generic port boundary only.
