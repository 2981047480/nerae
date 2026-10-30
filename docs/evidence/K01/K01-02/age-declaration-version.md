# 18+ 自我声明的版本化合同

`AgeAttestation` 要求用户明确提交 `isAdult: true` 和从当前账号资料获得的 `expectedVersion`，范围为 `1..2^53-1`。`User` 增加可选的 `ageDeclarationVersion`；版本缺失时客户端不能凭本机猜测来声明。冲突或结果未知时先重新读取当前状态，不能自动重新声明。自我声明不构成身份或精确年龄核验。

- `api/tests/run_contract_tests.py --summary`：101 项通过，0 跳过，含声明版本必填、边界、拒绝声明和额外字段的正反例。
- `scripts/codegen/generate.py write`：锁定 Go 1.27.3 / Node 26.0.0 生成两份相同 Go 与 TypeScript 输出，28 项必需生成/schema 检查通过。无隔离 DSN 时数据库测试按设计跳过。首次受限沙箱及其诊断重跑因本机 SMTP 测试绑定 `127.0.0.1` 被拒绝而失败；在获授权的本机环境运行退出 0。
- nullable 清单与此前相比只有合同哈希变化，字段数和内容未变化。

合同与生成类型不代表 HTTP、客户端页面或真实用户声明已完成；实际响应与声明处理需要单独验证。
