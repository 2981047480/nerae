# D01-03 fix03 候选报告（待 Lead 冻结复验）

状态：**candidate / 尚未 accepted**。基线为 Lead 冻结 `a289b090b5b172cf027b37dd0d5535659dd68364`；本轮在其上继续，未改 Git HEAD、未回滚 aaf/6a，4188 固定验收构建与 4186 首批预览均未触碰。55 项/16 族映射沿用 [implementation-map.json](implementation-map.json)，原 D01-03、fix01、fix02 报告保留。预览 `http://127.0.0.1:4187/`，PID 64273。

## 处理

- **EA28 / EA29**：把读者角色与引用上下文持久化到 `SelectionState`（actor、incoming studentId/version/readOnly/refMissing、recordsFocus）。`messages`/`incomingPlan` 共享屏导航与 `goBack` 保留当前读者；冷加载从 selection 恢复，不再默认回林澈 latest。实际 open→刷新→返回：学员林澈旧 v7 仍为学员林澈 v7；教练周岚旧交付仍为教练、周岚、只读；缺失引用刷新仍“引用不可用”；训练引用刷新仍展开原 10/22 记录。只读/历史入口的 `openAdoptSheet`/`confirmAdoption` 拒绝采用。
- **VR13**：新增 `coach-messages` 预览情境；先选教练再点第二个“消息”进入教练列表，不切回学员。
- **VR14**：320/200 的 `change-summary-head` 改为纵向叠排，标题与“对照现用版本”合理换行；实测标题宽 214、高 37.5，非 51×225 竖排，未缩字号或截字。

## 验证

最新同版：typecheck **0**、build **0**、vitest **95/95**、exit **0**；日志 `logs/d01-03-fix03-*`。390×900 CDP 实操 open→reload→back（学员/教练/缺失/训练）、教练消息入口；320×900/200 审阅标题。模块为 vitest/jsdom，浏览器为 CDP，两者区分；以上不等于 Lead 冻结版验收。

## 边界

引用版本是“所属学员 + 版本”的不可变快照，不是跨学员全局 ID；不同学员同号版本不会互相打开。旧本地状态缺少新增选择字段时按合法默认迁移，无法形成可信引用时显示不可用，不猜成林澈当前计划。只持久化选择与引用，不持久化弹层、确认 hash 或动画瞬时状态。未发现本包范围内未解决代码项；未完成项均为 Lead 的全矩阵/真实服务验收。

## 散列与未测

源码清单 `logs/d01-03-fix03-source-sha256.txt`，sha256 `b874c208…`；证据清单 `logs/d01-03-fix03-evidence-sha256.txt`，sha256 `b51f8451…`；持续 dev 日志排除。未测：Lead 全 16 族/五宽/200 全屏/动效、真实服务端权限/消息/健康鉴权、微信/uni-app 真机、真实 AI/支付。完整目标仍 active，请 Lead 冻结后独立复验。
