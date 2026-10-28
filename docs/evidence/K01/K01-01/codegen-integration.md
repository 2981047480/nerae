# 交给 F02 的契约生成集成请求

状态：待 F02 集成，不是正式配置变更或任务接受。触发证据：以 `oapi-codegen` 2.4.1 的默认 nullable 行为生成 `WorkoutFinish` 时，`durationMin`、`rpe`、`note` 无法可靠区分未提供与明确 null。记录修订要求前者保留已有值，后者清除；混淆会造成意外清空。原 `api/oapi-codegen.yaml` 暂未更改。

Lead 已用 `api/tests/codegen/oapi-codegen-probe.yaml` 的 `nullable-type: true` 生成模型、Gin 接口及内嵌契约，并使用 `api/tests/codegen/check.sh` 验证 TypeScript 可构造记录/批次/详情、Go 生成物可编译、总时长省略/null/值三态可分别解码和编码，以及 `kin-openapi` 对记录组合的正反例。探针以 Go 1.27.3、`oapi-codegen` 2.4.1、`openapi-typescript` 7.6.1、TypeScript 5.9.3、`kin-openapi` 0.127.0 和 `nullable` 1.1.0 运行；所有已有接口的行为兼容性和实际 HTTP 尚待复验。

F02-01 实施前由 Lead 串行处理 `api/oapi-codegen.yaml` 的共享变更，并评估启用该选项对全部 nullable 字段、Go 模型和依赖锁的影响。应将此目录的 TS/Go 检查接入正式生成验证，使用真正的生产生成配置再次测试三态；再对影响面内实际接口执行正反例与消费者编译，防止类型可编译但语义错误。最终工具和库版本、配置及生成输出须由正式工程固定，不以临时缓存为锁。

Go/TS 生成器不会自动实施记录 schema 的 `not` 等组合约束。生成的 Go 写类型仍能持有只读 `resolvedExerciseName`，生成的读类型对部分 readOnly 必填字段仍可能使用可空指针；TypeScript 写类型也不能凭静态检查阻止所有伪造值。因此服务端必须对请求和响应执行正式合同校验，并由 HTTP 测试验证服务端拒绝伪造名称、非法动作/度量组合，以及详情缺少必需名称/时间。跨行键、事务、幂等和日期规则另按 [运行时案例](runtime-cases.md) 测试。不得依赖类型或成功生成代替这些检查。
