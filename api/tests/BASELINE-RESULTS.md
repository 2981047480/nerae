# 静态契约回归：原稿 RED 记录

## 输入与执行

- 日期：2026-10-22（Asia/Shanghai）。
- 输入：[Lead 保留的原稿](<../../.review/contract-fix-20261022/baseline-openapi.yaml>)；与[审核报告](<../../docs/ER与API审核报告-v1.0.md>) SHA-256 一致。
- SHA-256：`809edbe3d63a8956a01f14ae8f80f9abe2ab5b5650850add75563f2cfbafce42`。
- Python 3.11.15；PyYAML 6.0.3、jsonschema 4.23.0，依赖及传递依赖固定在[requirements](<../test-requirements.txt>)。
- 首次实际回归版本：25 个 unittest 方法；2 通过、23 失败；包含子用例共 188 个 assertion failure、0 error、0 skip；退出码 **1（预期）**。
- 该结果是在 Lead 提交新稿后、切换测试输入到新稿前，对未变的原始快照实际执行得到；不是根据审核报告估算。

```sh
PYTHONDONTWRITEBYTECODE=1 \
NERAE_OPENAPI=.review/contract-fix-20261022/baseline-openapi.yaml \
api/tests/.venv-py311/bin/python api/tests/run_contract_tests.py --summary
```

## 失败与审核缺口对应

| 原稿问题 | 实测失败 |
|---|---|
| 全部操作缺 operationId | 83 个操作子用例失败 |
| 操作未描述统一 4xx | 59 个操作子用例失败 |
| 幂等键允许空字符串 | 6 个使用该参数的操作失败 |
| Parameter Object 上写 default | 价格列表的 sort 参数失败 |
| YAML 字符串价格枚举被转成整数 | `299_499` / `499_799` 类型检查及字符串正例失败 |
| 服务关系无法发起、确认/结束/授权无 expectedVersion | 关系创建、双方确认、结束、授权撤回测试失败 |
| 首批计划交付、点评、咨询会话缺入口 | 新增路径探测失败 |
| TrainingDay 无 id；开始训练不锁版本和日 | 读取 ID 与计划/自由训练互斥测试失败 |
| 训练/饮食未定义结构化输入联合，私人饮食复用公开模型 | 联合内容与私人编辑/响应测试失败 |
| 服务包、饮食打卡接受空对象 | 2 个空对象负例失败；个人新建计划拒绝空对象已通过 |
| 没有持久发布草稿、内容版本/确认绑定 | 草稿入口、发布精确请求、AI 溯源与内容测试失败 |
| 受信任标签未标 readOnly | 可信标签测试失败 |
| 伤病输入未标 writeOnly、读取未拆分 | 伤病隐私测试失败 |
| 饮食证据/规则版本/信息不足约束缺失 | 异常评估与自报证据测试失败 |

通过的 2 项仅是 OpenAPI 3.0 版本/本地引用完整性，以及组件 schema 的基础 Draft 7 语法兼容检查。**它们并不表示业务契约完整。**

## 最终套件复验原稿

测试扩展并按 Lead 新稿实际字段定稿后，仍用同一原稿 SHA 复验：**53 个方法执行、43 个方法失败、330 个断言失败事件、0 error / skip，退出码 1（预期）**。这包含 8 项验证器自检；未将子用例数误记为独立业务缺陷。第一次 RED 的 25/23/188 历史计数保持不变。最终新稿结果及 SHA 见[测试说明](<README.md>)和[机器摘要](<results.json>)。

## 工具问题与业务 RED 分开

在本次真实回归前，bundled Python 3.12 的签名限制使 `rpds` 动态库无法加载；另一个 Python 3.13 的 venv 缺 `encodings` 导致启动失败。它们都没有进入契约断言，**不计入上述 RED**。改用可运行的 Python 3.11.15 隔离环境后完成上述执行；未修改系统签名、全局依赖或 OpenAPI。

jsonschema 默认依赖不含 RFC3339 date-time checker；已额外固定 `rfc3339-validator==0.1.4` 和 `six==1.17.0`，防止非法时间正好被忽略。

## 解释边界

这是静态 OpenAPI / JSON Schema 正反例回归。没有发送 HTTP，没有后端/数据库，也未证明并发版本比较、幂等重放、跨关系授权、撤权即时失效、内容哈希一致性、AI 不捏造或营养判断正确。新增迭代测试的最终统计另见[测试说明](<README.md>)；本记录保留第一次 RED 的原始计数，不随测试扩展改写历史。
