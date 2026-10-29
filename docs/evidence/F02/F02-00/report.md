# F02-00 共享 Go 生成配置进度（未接受）

状态：`partial`，任务保持 `active`；K01 尚未接受，最终合同变化后仍须重新盘点和复验。执行者 Project Lead，实际模型和推理档位未由运行时报告；任务基线 `7621f1cfc590616cc343bca72f23909b17d9bbfd`。本次验证的集成提交 `077c7284675a7253164c07a1fe2427eaf6446439`。

正式 `api/oapi-codegen.yaml` 启用 nullable 三态，并通过固定版本模板将可空类型指向本地 `contractnullable`。对带 `omitempty` 的可选字段，省略/null/值保留不同语义；对无 `omitempty` 的必填 nullable，未设置时 JSON 编码失败，显式 null 或具体值才可编码。这样避免上游默认将未设置的必填数字/字符串编码为 0/空串。模板和数据结构的上游来源、变更及 Apache 许可证保存在 `api/templates/`。

当前 OpenAPI SHA-256 `8ee8e9af3a5cfaef4d38dad19b70629ca1c162990c29eb12e77a25c6257b46e6`；递归清单覆盖 64 个 nullable 属性（组件 49、路径内联 15；必填 14、可选 50），详见 `nullable-inventory.json`。合同或清单有变化时 `verify.sh` 先失败，要求重新盘点；清单变更失败注入已执行。测试使用 Go 1.27.3、oapi-codegen 2.4.1 和固定 probe 模块依赖，实际生成两次并逐字节比较；13 个非只读必填 nullable 字段的未设置/null 正反例，以及 WorkoutFinish 的时长、RPE、备注各自省略/null/值编解码均通过。`WorkoutSessionDetail.recordedAt` 和 `WorkoutSetRecord.resolvedExerciseName` 缺失时，直接 schema 验证会拒绝；明确 null 或合法名称的相应样例通过。

集成版本执行 `docs/evidence/F02/F02-00/verify.sh` 退出 0：清单漂移反例 1/1、两次正式配置生成一致、生成模型及包装器 race 测试通过。`make check-foundation` 在同版本退出 0，任务图 51 项、静态合同 97 项，以及基础代码、类型、三端资源与后台构建检查通过。独立 reviewer `/root/contract_tests` 在同一集成提交单独复跑 `verify.sh` 和任务图 51 项，未发现 P1/P2；复核产物保留在其实际输出的临时目录。

此前集成提交 `beadd6b` 的基础检查因新增任务后 M1 工量文档与旧测试未同步而退出 2；已在 `077c728` 更新为各表格行动态校验，补数字错位反例并完整重跑。工量仅为有效人日粗估，当前 M1 合计 49.5–96、另加 20% 预留约 59.4–115.2，不是工期承诺。

边界：只读必填字段可能被生成器标记为 `omitempty`，因此后续 F02-02 必须在真实响应路径执行 schema 校验；Go 解码本身也不会拒绝遗漏必填请求字段。上述直接模型/schema 测试不证明服务端中间件、HTTP、数据库或权限已接线。正式 TS/Go 生成流水线与生产消费者编译属于 F02-01，尚未完成；不可因此标记 F02-00/F02 已接受。
