# A04-01 Ordered batch 领域实现

状态：partial；纯领域排序入口与 race 单测完成，未声称 HTTP/DB 或跨端验收。

基线：`ed9c68a9c78a343199be2e4f1579f643062dc78e`。实际模型/推理：gpt-6-luna / medium。仅新增本报告和 `apps/api/internal/modules/workout/domain/ordered_batch.go`、`ordered_batch_test.go`。

实现新增 `ApplyOrderedBatch`，要求传入非 nil 的完整当前/目标动作条目顺序。先核验当前 Session 的状态、组键与快照映射、条目映射、每条目 setIndex 唯一性和旧顺序完整性，再通过既有 `ApplyBatch` 在副本上应用变更，最后核验新快照与目标顺序严格一一对应。排序-only 请求不走旧 `ApplyBatch` 的空批次拒绝路径；空集合可用显式空 slice 表达。目标顺序以参数为准，不从 map 或时间猜测。相同动作的不同 entry key 保持区分；不同条目可有相同 setIndex。上限 2000 个条目；无效结果不改变输入。

测试覆盖：部分更新保留其他组、重排与同批 setIndex 交换、排序-only、显式空集合、nil/额外/缺失/重复/大小写不同的顺序项、组索引冲突、删除和恢复时重排、非法状态、2000/2001 条目边界及失败不修改原快照。初测发现未检查 2000 条目上限，已修复。

验证：
- `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -w apps/api/internal/modules/workout/domain/ordered_batch.go apps/api/internal/modules/workout/domain/ordered_batch_test.go`：完成。
- `cd apps/api && GOTOOLCHAIN=local GOPROXY=off /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/workout/domain`：exit 0。
- `git diff --check`：exit 0。

边界：这是内部纯领域入口。仓储顺序持久化、乐观版本和真实数据库原子性由 Lead 后续接入；未运行其他模块或 HTTP/DB 测试。

## 旧快照完整性复核修复

新增 `ErrInvalidOrderedSnapshot` 区分旧持久化快照损坏与用户目标顺序无效。验证旧快照时反向检查每个活动键都有实际组且条目一致、删除键没有活动组且条目仍存在；`completed` 无实际组拒绝。目标顺序错误仍映射为 `ErrInvalid`。测试还覆盖投影后重复 setIndex，以及相同动作名称但不同 entry key 保持独立。测试辅助代码改为指针修改快照，确保 completed 空组反例真实生效。

指定命令 `GOTOOLCHAIN=local GOPROXY=off /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/workout/domain` exit 0；`git diff --check` exit 0。

## 错误分类最终复核

nil/非法 `currentOrder` 与非法持久化状态现在返回 `ErrInvalidOrderedSnapshot`；nil `targetOrder` 返回 `ErrInvalid`。新增断言直接区分三种结果。completed 空组反例以空 current/target order 验证；删除键缺条目反例先移除活动组并用空顺序验证，避免顺序错误掩盖快照错误。

最终指定命令：`cd apps/api && GOTOOLCHAIN=local GOPROXY=off /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/workout/domain`，exit 0。

## 历史条目身份复核

新增对全部历史 `Entries` 的稳定键和 `Exercise` 来源身份校验，覆盖仅被已删除键引用的条目。补充恢复负例：删除条目快照中的历史动作来源无效时，旧快照阶段返回 `ErrInvalidOrderedSnapshot`，不会落入用户恢复请求错误分类。指定领域 race 测试 exit 0。
