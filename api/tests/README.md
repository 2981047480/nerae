# OpenAPI 静态契约回归

**只验证契约静态约束，不是 HTTP、后端、并发或授权集成测试。** 不修改 [OpenAPI](<../openapi.yaml>)，也不启动服务。

## 执行

从仓库根目录运行。需要 Python **3.11+**；本次实测 Python 3.11.15。所有依赖（含传递依赖）固定在 [requirements](<../test-requirements.txt>)，不要全局安装。

```sh
python3.11 -m venv api/tests/.venv-py311
api/tests/.venv-py311/bin/python -m pip --isolated --disable-pip-version-check \
  install --no-cache-dir --keyring-provider disabled -r api/test-requirements.txt

PYTHONDONTWRITEBYTECODE=1 api/tests/.venv-py311/bin/python \
  -m unittest discover -s api/tests -p 'test_*.py' -v
```

可用[摘要运行器](<run_contract_tests.py>)输出 JSON 到标准输出：

```sh
PYTHONDONTWRITEBYTECODE=1 api/tests/.venv-py311/bin/python \
  api/tests/run_contract_tests.py --summary

# 复验原稿：应退出 1，不可用 “|| true” 掩盖回归状态。
PYTHONDONTWRITEBYTECODE=1 \
NERAE_OPENAPI=.review/plan-repair-20261023/baseline/api/openapi.yaml \
api/tests/.venv-py311/bin/python api/tests/run_contract_tests.py --summary
```

默认读[当前契约](<../openapi.yaml>)；`NERAE_OPENAPI` 仅用于明确指定待检验的本地契约快照。测试不会请求 HTTP、下载远程 schema、读凭据、生成代码或写报告；运行器也只输出标准流。虚拟环境被[测试目录忽略规则](<.gitignore>)排除，不是交付源码。

## 最新实测

2026-10-23（Asia/Shanghai），OpenAPI 1.2.0：

| 输入 | 结果 | 解释 |
|---|---|---|
| 当前契约，SHA `c917575ee3ac8552e954809355c8eee18e3316d6daae934bbbb93a33d071e53e` | **72 方法通过，0 failure / error / skip** | 原 53 项 + 19 项方案修复回归；退出 0 |
| 修复前 v1.1，SHA `fd2c2a193684326de740d8aa40719980f3cd0727e261eec9409bc1e71f8a8481` | 72 方法执行，19 方法失败，0 error / skip | 预期 RED，退出 1；[反例摘要](../../.review/plan-repair-20261023/baseline-countercheck.json) |

当前机器摘要见 [contract-tests.json](../../.review/plan-repair-20261023/contract-tests.json)，独立 lint/代码生成/编译和参考稿检查见 [修复对照](../../docs/方案修复对照-v1.2.md)。结果与 SHA 绑定，不自动代表之后的修改。

### 历史实测（保留原始结果）

以下为 2026-10-22 的 v1.1 结果，**不是当前契约的测试数量或摘要**：

| 输入 | 结果 | 解释 |
|---|---|---|
| 修订契约，SHA `fd2c2a193684326de740d8aa40719980f3cd0727e261eec9409bc1e71f8a8481` | **53 方法通过，0 failure / error / skip** | 45 项契约回归 + 8 项验证器防假绿测试；退出 0 |
| 同一最终测试集回测原稿，SHA `809edbe3d63a8956a01f14ae8f80f9abe2ab5b5650850add75563f2cfbafce42` | 53 方法执行，43 方法失败；330 断言失败事件，0 error / skip | **预期 RED，退出 1**；其余 10 方法为基础有效性及测试工具自检 |
| 首次原稿测试集 | 25 执行，23 方法失败，188 断言失败事件 | 原始 RED 保留于[基线说明](<BASELINE-RESULTS.md>) |

失败事件包含 subTest，**不是独立缺陷数量**。历史机器摘要见[结果快照](<results.json>)；摘要仅证明对应 SHA 的执行结果。

## 覆盖与测试边界

| 领域 | 静态约束与正反例 |
|---|---|
| 基础契约 | 唯一 YAML key、本地 `$ref`、组件 schema 兼容性、operationId 非空唯一及既有映射稳定、每个操作统一 4xx、Parameter default 层级、字符串 enum 无 YAML 数字/布尔歧义 |
| 幂等 | 请求头存在于关键写操作；空字符串/非字符串不能充当键，不模拟重复请求 |
| 服务关系 | 创建必填学员/时间/条款；完整条款正例与空/伪造状态负例；确认绑定 expectedVersion、termsHash、acknowledged；结束/授权写版本；空 scopes 是合法撤回 |
| 教练交付与沟通 | 建立/编辑/发布、学员读取、评论和预约消息入口；训练/饮食正例；缺字段、空正文、伪造作者/关系/认证字段负例 |
| 训练与私人计划 | TrainingDay 只读 id；计划训练 pin planId/version/day，自由训练独立 oneOf；结构化训练/饮食联合；私人饮食可编辑且不复用公开模板返回；publishedVersion 字段和未发布草稿隔离**说明** |
| 内容细节 | 独立虚构训练处方、餐次、份量、单位、过敏原未知状态正例；空数组、缺份量、混合联合、错误 kind、伪造读取 ID 负例 |
| 发布 | 手工草稿创建/读取/版本更新；editing/blocked 可缺结构化内容，ready/submitted 必须完整；visibility 与 hash 覆盖**说明**；发布仅 draftId/version/visibility/confirmation；确认必填且 acknowledged=true |
| AI 草稿 | draftId/draftVersion/aiRunId；信息不足允许省略内容而保留待补字段，存在内容时必须符合联合；AI 溯源与未知状态；可信标签只读 |
| 健康隐私 | injuryNotes 写入 writeOnly、显式 null 可清除，读取 schema 不含原文；拒绝 public-health 与公开健康开关；过敏原同意撤回 DELETE 带版本 |
| 饮食证据 | 打卡绑定 planVersion/dayIndex、允许仅自述未知餐次不伪造摄入；拒绝客户端随意填实际热量；异常含 insufficient_data、证据引用、规则版本；不把自报当成测量事实的**说明** |

[结构测试](<test_static_contract.py>)通过路径和局部引用查询模型，辅助查询封装占位符及可接受的模型/属性别名；未硬编码 schema 总数。[字面正反例](<test_json_examples.py>)使用[独立合成数据](<examples.py>)，不是从 schema 自动生成一份必过样本。数值只用于类型/范围测试，不是健康建议或安全阈值。

[operationId 清单](<operation_ids.json>)固定 Lead 新稿的实际公开 ID，**不按测试自行发明命名规则**；允许新增操作，不锁操作数量。既有操作重命名/删除需要显式审核并同步清单；单纯给每个操作换一个仍唯一的 ID 不能逃过回归。

## JSON Schema 适配不等于服务端行为

[v1.2 修复测试](test_plan_repair.py)新增动作冻结快照、草稿回读、受限学员资料、AI 状态联合、日程与当前安排独立版本、改期、原子开始、关系消息/撤回占位、模板建议确认的 19 项正反例。训练读样本独立包含快照；写样本继续拒绝快照。普通消息与健康消息 scopes 互斥，客户端字段不被当作真实权限。日期推演、事务原子性和服务端内容分类仍需运行时验证。

[辅助模块](<contract_support.py>)使用 PyYAML 6.0.3 与 jsonschema 4.23.0 / Draft 7：

- 只做 OpenAPI 3.0 `nullable` 和布尔 `exclusiveMinimum/Maximum` 的语法适配，保留 oneOf、allOf、required、additionalProperties；不凭空加业务约束。
- `nullable` 不绕过 enum；没有显式 type 的 nullable 不吞掉 allOf 限制。信息不足用省略字段表达，不强迫 OpenAPI 3.0 的 nullable+oneOf 模式。
- `readOnly` / `writeOnly` 在 jsonschema 中只是注释。测试另外检查标记和读写 schema 的字段可达性；不会误称这些标记已经执行了权限控制/响应过滤。
- 固定安装 RFC3339 checker，否则 jsonschema 可能静默忽略 date-time。独立[工具自检](<test_validator_support.py>)验证非法日期确实失败、联合互斥、闭合对象、引用和重复 YAML key。

## 尚未验证的风险

1. **所有运行时授权与并发**：JWT/角色验证、跨教练/跨学员隔离、授权撤回即时生效、关系结束后恢复授权防护、后台任务重查、expectedVersion 原子比较，以及 publishedVersion 实际投影。
2. **幂等协议**：主体/操作隔离、同键同内容重放、同键异内容冲突、过期保存和离线并发。本测试只查头部及可表达的数据约束。
3. **跨字段/跨记录一致性**：起止时间顺序、套餐版本和条款、planType 对 content.kind、餐次 slotKey 属于所选历史版本、AI 引用与主体归属。
4. **发布确认与 AI 安全**：hash 与数据库草稿内容/visibility 的确切一致、估算字段覆盖完整、状态机执行、模型不捏造/不泄漏。有效 JSON、人工勾选或身份标签均不证明专业审核与健康安全。
5. **证据推理**：observed_concern 所需真实且有效授权的证据、unknown 状态组合、数值/营养规则专业适用性。必填 evidence/ruleVersion 不证明证据充分或判断正确。
6. **后端/数据库/代码生成**：HTTP 返回码与实际响应、DDL/事务/历史快照、正式 OpenAPI lint、TypeScript/Go 生成及编译不由本套件验证；后几项由 Lead 单独执行和汇报。

没有把接口存在、描述存在或 JSON 校验通过报告成线上行为已经验收。
