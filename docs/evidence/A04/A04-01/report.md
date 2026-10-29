# A04-01 领域规则实现报告

状态：partial（纯领域逻辑与单元测试通过；HTTP、数据库事务、权限、并发及集成尚未实现/验证）

- 实际模型/推理：gpt-6-luna / medium；角色 worker。
- 任务卡基线：`620befb974e2da8a3922494ebe32127229bb1ac9`。开始时工作树 HEAD：`9066325a57869f76824e5f17c5a1cd9ef31bd14d`，其中任务图较基线有 Lead 已登记改动；本任务未改任务图。
- 写入仅在 `apps/api/internal/modules/workout/` 与本证据目录。

## 实现

- 领域校验覆盖 OpenAPI 七类 kind、kind 对应的重量/次数/手填秒数组合、范围和小数精度；十进制使用字符串和 `math/big.Rat` 检查，不通过浮点舍入接受非法精度。
- 校验自定义/库动作来源结构和稳定键格式，并冻结同一 `exerciseEntryKey` 的身份。库动作 ID 只做格式检查，绝不代表使用授权；动作目录授权必须由应用/服务层检查。
- `ApplyBatch` 校验组键重复、更新/删除重叠、明确恢复集合及已删除状态、恢复动作条目一致性、200 项上限和 skipped 会话限制。所有变更在副本上完成，失败返回零结果且不修改输入快照。未列出的删除键按契约反例作无操作处理。
- 删除最后一组使 completed 会话回到 `in_progress`；只有至少一组实际记录才允许 `Complete`。不在领域函数中授予身份、执行持久化或声称事务/幂等实现。

## 验证

| 命令 | 结果 |
|---|---|
| `go test -race ./internal/modules/workout/...`（PATH 默认 Go） | exit 127；环境无 `go` 命令 |
| `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -w apps/api/internal/modules/workout/domain.go apps/api/internal/modules/workout/domain_test.go` | 完成 |
| `cd apps/api && /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/workout/...` | exit 0；race 测试通过 |

测试覆盖七类度量、精度/边界负例、重复键、更新与删除冲突、恢复活动组/恢复已删组、最后组删除回草稿、空会话完成拒绝、skipped 会话拒绝写组及超量拒绝。首轮 race 测试发现活动组被错误接受为恢复目标；修复后复跑通过。

限制：单元输入中的 `Session` 是调用方提供的持久化状态快照；本包不能证明其来源可信，也不执行主体鉴权、会话版本比较、SQL 事务、数据库并发、统计投影、API 错误映射或真实回滚。上述仍须由 A03/F02 和集成任务验证。未运行其他包或数据库测试。

## Lead 复核边界修复

复核指出三处问题，已在 `domain.go` / `domain_test.go` 修复：十进制文本先限制为最多 16 字节的普通数字/单个小数点/可选负号语法，再进入 `big.Rat` 范围比较，拒绝分数、加号、十六进制、指数及超长输入；`Complete` 拒绝未知状态；稳定键用有效 UTF-8 rune 数按 8–64 字符校验。新增对应负例和 8-rune 多字节键边界测试。

复核后命令：`cd apps/api && /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/workout/...`，exit 0；`git diff --check` exit 0。仍仅为纯领域单元验证。

Lead 后续核对 OpenAPI 后将同一动作条目来源或名称变化归为 `ErrInvalid`，以便接线时按合同映射 422；补充负例并再次运行 race 测试 exit 0。HTTP 错误映射本身尚未实现。
