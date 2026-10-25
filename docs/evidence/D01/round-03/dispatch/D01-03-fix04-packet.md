# fix04 · 只收束引用返回与旧缓存

原第7轮已正常交回；Lead原样冻结新基线 **55994603461ee5710a5be4f70b1180d445dba5c5**。保持原Harness会话/OpenCode Go/opencode-go/deepseek-v4.1-flash/Medium、树 `/private/tmp/nerae-ui-complete`、4187；不改Git HEAD、依赖、风格、产品范围或固定预览日期（仍2026-10-24）。只修下面原EA03/EA28/EA29未覆盖的返回/迁移。

先读主仓 `docs/evidence/D01/round-03/lead/REVIEW_08.md`。类型/构建/97测试过，Lead修正夹具后21独立检查17过4败，精确输入、actual/expected在 `lead/expansion/frozen-04/code-reference-followup/identity-seams.json`；检查执行源码也在同目录。不是要求你重跑全部UI。

## 四个实际模块反例

1. 学员GoConversation Gu→open training `record-20260929`→冷恢复recordId正确→goBack实际today，expected原messages/Gu。`openMessageRef(training)`不可用会root/清stack的普通 `goToRecords` 代替引用导航。直接主导航记录页仍应是root；引用要保留来处和对象。
2. 旧缓存 `screen=messages, actor=coach, selection.activeConversationId=conv-zhou-lan`，只有selection.actor缺失（旧版没有该字段）。冷加载实际student，expectedcoach。base默认先合并导致 `merged.selection.actor ?? parsed.actor` 总取student。用原始字段存在性/合法值和可信旧actor迁移，不能从共享screen猜角色。
3. 旧缓存 `screen=incomingPlan, actor=coach, stack=['messages']`、周岚上下文，但selection缺新增incoming owner/version/readOnly/refMissing。实际student+林澈latest7+可采用。无法恢复原引用时明确不可用、可返回，保留可信读者；不能猜成任何学生latest，不持久化弹层/确认hash。已经被先前版本补过默认值的“消息来源+无引用版本”也不因此变可信；按引用上下文不变量校验。
4. 旧 `screen=records, stack=['messages']` 的引用缓存缺新增recordsFocusId/Explicit，实际explicit=false落普通默认展开。无法确定原记录就呈现不可用/不展开错误记录。正常直接进入记录（不是引用）仍可按默认最近记录展示；不要一刀切阻断所有旧缓存。

保持新格式的已通过行为：教练/学员原引用open→reload→back，周岚/林澈同号版本隔离，只读/历史动作层禁止采用，缺失不fallback；也保留已经补好的M02/M04/M05、320/200标题与教练消息入口。

只需相关store/类型/局部视图（确有必要时）及有意义正反例；至少验证合法旧coach messages、未知旧plan/training引用、新格式非默认record返回与冷恢复、正常非引用旧入口。一次最终type/build/全部测试与定向浏览器检查。

写域仅prototype与worker；新 `D01-03-fix04-report.md/json` + source/evidence/report三SHA清单，保留旧报告和真实未测/退出码。Lead原样冻新源后最终复验/集成；你不改Lead证据/任务、不做全矩阵、不扩新功能。4186首批及4188/4189 Lead固定构建不可停止/占用。完成后正常交回并停止源码变动。
