# 记录与身份合同生成探针

这是 K01 记录合同的独立生成和编译检查，不是应用、HTTP、数据库或正式契约生成流水线。它在临时目录生成 TypeScript 和 Go 模型、Gin 接口和内嵌契约，编译 `recording-types.ts`、`identity-types.ts` 并运行 Go 模型与 `kin-openapi` 规格组合正反例。身份探针仅验证生成类型和部分 schema，不验证短信、签名、授权、单次消费或撤权。实际服务仍必须挂载并验证请求/响应校验：生成的静态类型不能表达动作来源 XOR、按 kind 限制字段、组键跨行关系或所有状态规则。

2026-10-28 验证环境：Node 26.0.0、`openapi-typescript` 7.6.1、TypeScript 5.9.3、Go 1.27.3、`oapi-codegen` 2.4.1。Go 探针模块固定 `kin-openapi` 0.127.0、`nullable` 1.1.0 和 `runtime` 1.1.1；它不是生产应用的依赖锁，也不代表完成依赖安全审计。校验脚本默认从 PATH 查找工具，也可分别通过 `NERAE_OPENAPI_TYPESCRIPT`、`NERAE_TSC`、`NERAE_GO`、`NERAE_OAPI_CODEGEN` 指定可执行文件。应自行核验工具版本，并根据环境提供网络或已填充的 Go 模块缓存。

```sh
api/tests/codegen/check.sh
```

Go 探针配置启用 `nullable-type: true`，以区分未提供、明确 null 和具体值；`api/oapi-codegen.yaml` 的正式配置尚未修改。F02 要按 [集成请求](../../../docs/evidence/K01/K01-01/codegen-integration.md) 在真实生成配置与工程中验证，不能把此探针当作 F02 或运行时完成。
