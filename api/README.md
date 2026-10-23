# API 契约说明（修订 v1.2.0）

契约：[OpenAPI 3.0.3](openapi.yaml)，当前版本 1.2.0，99 路径、123 操作。模型依据：[ER v1.2](../docs/数据模型与ER设计.md)、[领域词汇](../CONTEXT.md)、[用户流程](../docs/APP_FLOW.md)；修复与验证见[本轮报告](../docs/方案修复对照-v1.2.md)。

**这是可校验、可生成类型的设计稿，不是已实现服务器。** v1.0/v1.1 尚未投产，本次直接修正设计缺陷；不保证与旧草稿生成物兼容。投产后破坏性变更须迁移或升接口大版本。

## 1. 通用约定

| 项 | 约定 |
|---|---|
| Base URL | `servers` 含 `/v1`，每条 `paths` 不重复该前缀；示例域名尚未部署 |
| 用户/后台身份 | `nerae-users` / `nerae-admin` 两类 audience 与独立后台身份；客户端角色仅改变界面，不授予认证、教练或管理能力 |
| 资源授权 | 从认证主体验证归属、有效服务关系、细分 data grant 和资源版本；不信任请求中的 actor/sourceTag 等身份背书 |
| 公开读取 | 公开列表仅已发布且 public 内容；link 内容用不可猜测分享令牌，不能靠猜数字 ID 读取 |
| 时间与 ID | ID 为字符串；时间 UTC ISO8601；打卡 date 按 timeZone，计划 dayIndex 是计划周期位置，不等于日历日期 |
| 错误 | 统一 `Error`：code/message/requestId；400 请求语法，401 登录失效，403 权限，404 不存在或需隐藏的资源，409 状态/幂等冲突，412 旧版本，422 内容约束，429 额度，503 上游暂不可用 |
| 乐观版本 | 写入携带 expectedVersion；关系、授权、私人计划、草稿、日记各有独立版本，不能相互替代 |
| 幂等 | 标明头的写入必须提供 `Idempotency-Key`；键按主体+方法+路径隔离；同键同摘要重放，同键异摘要409，并发只提交一次 |
| 幂等窗口 | 暂定24小时作为实现建议，投产前确认；成功响应头 `Idempotency-Key-Expires-At` 返回实际到期时间；过期后资源业务唯一键仍防重 |
| 健康数据 | 无 public-health 开关；profile不包含身体/伤病/饮食/过敏资料；diet不隐含allergy；伤病正文仅写入、普通档案不回显 |
| AI | 固定按已选 MiMo 配置，失败不自动升档收费模型；结构合法、作者确认、内容安全审核、专业审查是不同状态 |

## 2. 首批交付闭环（已补齐契约）

以下是应实现并测试的调用顺序，不是已经请求真实服务的记录。

1. 学员可匿名浏览；保存/咨询时登录并完成年龄与必要同意，不把完整身体档案设为全局前置。自主选择教练，`POST /appointments` 创建咨询；已有资料预填。
2. 双方通过 `GET/POST /appointments/{id}/messages` 联系；只看当前咨询单，不共享全部私信。
3. 已审核教练 `POST /coach/relations` 发起服务关系，服务器冻结服务期/套餐版本/服务说明。教练发起即代表其确认，返回 pending_student、version 和 termsHash。
4. 学员 `POST /relations/{id}/confirm` 确认同一 expectedVersion、termsHash，双方确认齐备后 active。不以成交自报替代。
5. 学员 `PUT /relations/{id}/grants` 授权；首次 expectedVersion=0，之后必须当前版本；scopes=[]明确撤回。结束关系与撤权原子生效，旧缓存/任务不继续读取。
6. 教练 `POST /coach/relations/{id}/plans` 建草稿，`GET /coach/plans/{id}` 回读，`PUT /coach/plans/{id}` 修改，`POST /coach/plans/{id}/publish` 人工确认当前版。列表由 `GET /coach/relations/{id}/plans` 恢复。
7. 学员通过 `GET /relations/{id}/plans` 和 `GET /me/plans/{id}` 读已发布版本。学员拥有记录不意味着可以读取未发布教练草稿；source=coach必须投影到publishedVersion。
8. `GET /me/today-training` 读取当前日程；首次开始使用 `POST /me/training-starts` 原子完成所需副本、安排与会话。`POST /me/sessions` 仍支持明确历史版本的计划训练和自由训练；自由训练不强造计划、日程或教练关系。
9. 按会话 version 保存组记录、结束训练；教练发点评，双方通过 `GET/POST /relations/{id}/messages` 回复，已读单独提交，不要求预先创建计划或咨询单。

关系结束不抹去学员自己的记录或历史评价资格；教练继续读取健康资料必须有当前有效权限。

公开陌生获客须先完成 L01 主体/类目/渠道证据与 V02 双端试验。用户已确认目前尚未注册或核定，本轮只建立条件与验收任务，不宣称联系渠道已获许可。

### 2.1 内容、日程与 AI 状态

- 训练读模型包含冻结的 `exerciseSnapshot`，动作库更新不回写历史；写模型不接受客户端伪造快照。动作库读取使用 `/exercises` 与 `/exercises/{id}`。
- 内容版本与日程分开；`selectionVersion` 控制唯一当前安排，`schedule.version` 控制暂停/恢复/结束，occurrence 具有自己的版本。固定日历、漏练不自动顺延、改期不修改实际训练日期；具体边界见 APP_FLOW F02。
- `clientEventId` 业务唯一键在幂等缓存过期后继续防止重复开始。接口头存在不代表原子事务已经实现。
- 教练 AI 结果为互斥 `ready / needs_input / safety_block`；只有 ready 返回持久化计划与完整读内容。技术失败用 503，额度用尽用 429，均不伪造成功计划。
- 关系消息由服务端重查成员、有效关系与细分授权；普通文字不能靠客户端标签获得健康访问权。撤权后隐藏健康正文与预览，保留原因占位。

## 3. 免费计划与发布闭环

### 3.1 私人计划与模板

- 广场模板使用 `templateId + templateVersion`；另存得到独立私人计划。
- 私人计划 `content.kind=training/diet` 采用不同具名结构，不把饮食塞进训练 days。
- 训练日读取返回稳定 ID；训练和打卡固定对应版本。
- 饮食份量可以通过 `PATCH /me/diet-plans/{id}` 编辑并生成新版本，原模板不变。
- 回滚复制历史内容为新的当前版本，不覆盖历史事实。
- 收藏和分享有独立接口；下架后分享令牌失效，不能经旧链接绕过审核。
- 模板内 AI 调整使用 `POST /me/plans/{id}/adjustments` → `GET /me/plan-adjustments/{id}` → `POST /me/plan-adjustments/{id}/apply`。确认绑定 baseVersion、建议 hash 与有效期；只对本人可编辑计划生效，重复应用仅产生一次新版本，不自动替换执行日程。

### 3.2 AI 预填与人工确认

- `POST /publish/assist` 返回 draftId/draftVersion/aiRunId、字段来源、缺失项和提示；ready才提供完整内容，信息不足时不编造份量/过敏原。
- 手动草稿用 `POST /publish/drafts`；两种草稿统一用 `GET/PATCH /publish/drafts/{id}` 读取与修改。
- 草稿默认 private；选择 public/link 前更新草稿。可见性和内容一起参与 contentHash，不能确认私密稿后悄悄改为公开。
- `POST /publish/plans` 仅提交草稿ID、版本、可见性与confirmation；服务器校验当前hash、全部待确认估算字段及发布主体。
- 改内容或来源元数据即使旧确认失效；最终模板版本关联准确草稿版本及确认记录。
- `sourceTag`、教练认证、证据等级由服务端产生；作者确认不等于内容经过专业健康审查。

## 4. 饮食记录与异常筛查

- `DietCheckinWrite` 至少包含 date、expectedVersion、planVersion、dayIndex、mealStatuses；首次写入expectedVersion=0，编辑需当前版本。
- 餐次用可重复安排的slotKey区分，例如两个加餐不会挤进同一个snack布尔值。
- `observations` 可选。仅自评是否按计划不能算出实际摄入；模型提取尚未确认的信息也不能当作已确认观察。
- `AnomalyFlag.assessment` 区分 potential_plan_risk / observed_concern / insufficient_data；证据带来源、版本与字段引用。
- 没有足够食物、份量、时间范围或授权，就返回信息不足；不把计划量当摄入量，不背书统一医学阈值。
- 教练通过关系范围内的 anomalies 读取并复核；运营读私人健康证据需专门授权，不因有审核角色即可查看全部健康资料。
- 过敏信息是可选且单独同意的资料；可通过 DELETE 撤回采集同意和请求删除，不能把“删除”解释成“无过敏”。

## 5. 验证与代码生成

所有命令从工作区根目录运行；固定工具版本避免结果漂移。

```bash
# 正式契约校验（不是仅检查YAML）
npx --yes @redocly/cli@1.34.3 lint api/openapi.yaml

# 隔离安装测试依赖，不改全局Python
python3.11 -m venv .review/contract-tests-venv
.review/contract-tests-venv/bin/python -m pip install -r api/test-requirements.txt
PYTHONDONTWRITEBYTECODE=1 .review/contract-tests-venv/bin/python \
  -m unittest discover -s api/tests -p 'test_*.py' -v

# 先生成到临时目录，不误当应用已经存在
mkdir -p .review/codegen
npx --yes openapi-typescript@7.6.1 api/openapi.yaml \
  -o .review/codegen/typescript.d.ts

# 真正开始Go工程时，先创建配置要求的输出目录
mkdir -p apps/api/internal/gen
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1 \
  -config api/oapi-codegen.yaml api/openapi.yaml
```

[生成配置](oapi-codegen.yaml)生成 Gin 接口、模型和嵌入规范，不生成鉴权、业务事务、数据库或内容安全实现。正式实施仍需请求/响应校验中间件和对象级授权。

2026-10-23 的结果位于 `.review/plan-repair-20261023/`：正式 lint 0 error/3 warning，72 项契约测试通过，TypeScript 与 Go 生成成功。Go 冒烟在该目录的 `go-smoke` 独立模块执行；`go test ./...` 的 `[no test files]` 仅证明生成物编译成功。依赖仅用于兼容性验证，正式工程版本由 F01/F02 锁定。完整命令、SHA 和限制见修复对照。

## 6. 已确认与尚未完成

### 本次修订已覆盖

- 规范错误、稳定operationId、错误响应、字符串枚举。
- 服务关系发起/双方确认/版本化授权/结束撤权。
- 教练发布计划、学员读取、训练日定位、点评、咨询单消息。
- 训练/饮食实例独立版本及编辑、公开模板版本、发布来源与确认链。
- 饮食自评和可选观察的证据区分、按授权筛查异常。
- 后台身份与凭据、跨账号幂等、私有健康资料读写分离。
- 动作读快照与目录、草稿回读、当前日程/应练日期、一步开始、关系双向消息、教练 AI 互斥状态和模板建议确认应用的契约。

### 不应声称已经完成

- 真实 HTTP 服务、数据库迁移、事务并发、JWT/对象级授权测试。
- 对模型健康安全、适用性、价格或 MiMo SKU 的生产验证。
- 周报自动化、图片打卡/上传凭证全链路、邀请码/海报、订阅消息授权、后台评价申诉完整工作流。
- 学员端 AI 模板内调整建议的真实“提出→确认→应用”行为：v1.2 已补契约，实际应用、确认哈希与并发仍待 B04/U07/C13。
- 正式服务器域名、对外许可URL与全部部署/合规手续。

未覆盖项需按依赖排期；不能再笼统称“都不影响核心”。部署前还必须验证真实用户的端到端流程与敏感数据负向用例。
