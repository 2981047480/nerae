# U02-01 · operation journal + secure storage 接缝测试

基线：`93a20b74d065abe2c408131e5bb032784b5c8c1c`（独立临时 clone，提交前未改集成树）。

本补充仅新增组合测试，直接把 `TrainingOperationJournal` 与现有 `SecureOperationJournalStorage` 接到同一合成 native CAS port。验证先持久化 `ready`，之后原子 CAS 为 `in_flight`，只有 CAS 成功才返回 dispatch permit；native CAS 在已改值后抛错或返回 unavailable 时，调用端拿不到 permit，记录保留原 body/key 并阻止新一轮发送。相同账号重新登录的新 generation 可读到 uncertain 数据，但即使测试时钟回拨也不能重发；其他账号只能看到自己的空事件键，原账号条目仍留在注册表。落盘检查确认原请求正文与幂等键会保留供核对，但认证 token、Authorization/Bearer 字段没有写入。

测试使用内存中的合成 native port 与合成 auth；并未调用 iOS Keychain、Android Keystore、原生 UTS、真实设备、HTTP、服务器或跨设备同步。它验证两个 TypeScript 模块之间的控制流和格式约束，不能证明平台 CAS 的硬件持久性、加密实现、跨进程并发、崩溃恢复或用户数据保护。

## 验证

- `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/operationJournalStorageInterop.test.mjs`：6/6 通过。
- `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/*.test.mjs`：146 项中 145 通过，1 项失败。失败为既有 `operationJournal.test.mjs` 用例 `raw responses and issued snapshots cannot settle without request-bound replay evidence`：其调用 `RecordingApi` 写请求未提供当前实现要求的有效 expiry，因而实际收到 `RecordingApiError: reconcile_required`（`recordingApi.ts:217,556`）。新增组合用例全部通过；此失败阻止本轮将训练 feature 全套声明为通过。
- `apps/client/node_modules/.bin/vue-tsc --noEmit -p apps/client/tsconfig.json`：退出码 0。临时 clone 未安装依赖；通过两个临时 symlink 使用已有 `nerae-client-recording` checkout 的 node_modules，命令后已移除这两个 symlink；未改锁文件或安装依赖。
- `git diff --check`：通过。

## 合成 RecordingApi 回执夹具修复

后续定位了上方全量训练测试的唯一失败：`operationJournal.test.mjs` 的 `matchingSnapshot()` 构造 `RecordingApi` 的成功 `POST /me/sessions` 响应时遗漏必需的 `Idempotency-Key-Expires-At`。这与当前 `recordingApi.ts` 的写响应契约不符；不是生产实现应放宽的行为。只在该合成成功响应中补上合法 UTC expiry，没有改断言或生产代码。

修复后验证：

- `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test --test-name-pattern='raw responses and issued snapshots cannot settle without request-bound replay evidence' apps/client/src/features/training/operationJournal.test.mjs`：1/1 通过。
- `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/*.test.mjs`：146/146 通过。
- `apps/client/node_modules/.bin/vue-tsc --noEmit -p apps/client/tsconfig.json`：退出码 0；使用上一节记录的临时依赖 symlink 方法，运行后移除 symlink。
- `git diff --check`：通过。

## 限制

失败或结果未知的 native CAS 永远按未知处理；若 in-flight 标记可能已提交，当前 journal 不自动重发，也没有服务端绑定回执来安全结算/清除记录。测试仅用同步合成 CAS 回调，未测原生异步桥、设备重启、多个 App 进程或实际 secure vault。训练草稿和 journal 的原子合并、HTTP 发送接线、服务端幂等回执及恢复仍未实现。
