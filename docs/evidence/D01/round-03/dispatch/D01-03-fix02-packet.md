# D01-03 fix02 · 全量冻结复验后的有界返工

上一轮 fix01 已正常交回，Lead 原样冻结为 `6a5609c5b0ecdb350be080350237f1a4e86d48b9`。从当前树继续，不 reset、不切模型、不重做视觉。仍只用同一 Harness 会话 / OpenCode Go / opencode-go/deepseek-v4.1-flash / Medium，workspace `/private/tmp/nerae-ui-complete`，branch `ui/full-round-three`，preview 4187。

写域仅 `design/prototype/**` 和 `docs/evidence/D01/round-03/worker/**`；不改 Lead、任务、契约、根依赖或 Git HEAD。Lead 只验收，不代写 UI。保持已有83测试及10项身份/授权/草稿接缝行为。

先读绝对路径 `/Users/zephyr/Documents/项目/nerae/docs/evidence/D01/round-03/lead/REVIEW_06.md`。所有本轮真实DOM/截图/矩阵位于该主仓库的 `docs/evidence/D01/round-03/lead/expansion/frozen-02/`。以下是已经观察的问题，不是新增产品范围。

## EA21 历史引用必须打开被引用的不可变版本

真实路径：教练林澈草稿加备注“第8版新增说明：先热身，再完成当前重量。”→保存8→发布8；学员消息仍同时有v7/v8按钮。点击旧的“计划 版本 7 · 力量基础 4 处调整 · 尚未采用”，实际打开v8和第8版新备注，可直接采用8，没有说明对象变了。`openMessageRef` 忽略ref.id，只打开latest incoming。

必须按ref稳定身份打开当时交付快照，旧版不可被新内容覆盖，也不可用旧确认采用另一份内容。若旧版已被替代不能采用，清楚说明并提供独立“查看最新版本”动作。训练/服务引用也按ID核对，不能总猜默认卧推或顾言。不要只把按钮标题改成最新来掩盖历史引用。

同一页面新v8仍写死“交付于10月23日21:40”，实际当天18:04交付。来源人物、标题、版本、时间从同一快照得到，保留UTC存储及固定安排日期。证据 old-v7-message-after-v8-delivery.txt / old-v7-message-opens-wrong-v8.txt / 对应settled截图 / lin-v8-delivered-v6-current.txt。

## EA22 同一个服务包的新报价不等于旧约定

服务月包699 v2→759 v3，保存范围2次、响应12小时；S16与S07正确保留原699/4周约定。学员从同月包咨询→展开服务与授权，却把759标成“已确认”，原699约定消失。当前只比较packageId，忽略已确认服务版本/条款快照。

按已确认快照显示原约定；当前报价名称/价格/单位/周期/范围/响应/版本变化时必须分开呈现，不得暗示双方已接受新价。覆盖同包变更及不同包咨询，旧字段未知仍诚实需核对；查看服务返回保持所选包与私有草稿。证据 same-package-new759-wrongly-confirmed.txt、service-v3-759-reload-old699-agreement.txt、student-coach-detail-saved-v3-quote.txt。

## EA23 记录空/单条时图表、摘要、明细必须同源

真实预览“无记录”显示明细0/还没有记录，图表仍有4点和+3kg，最近点评仍借旧记录；“仅一条”明细只1条，图表仍4点判断趋势。RecordsView displayRows处理empty/single，recordChart/summary/feedback没有同一投影。修成真实一致的空态/单条态，单条只显示一个点且不判断趋势；不造数据/不抹正常历史。证据 states-s05-01.txt/02.txt和对应截图。

## EA24 教练无结果状态应真实为空

发现的教练模式选择“无结果”，仍是3位及三人列表。让当前模式的加载/错误/空态与结果计数/恢复动作一致；实际筛选无结果同样检查，保留已选条件。不需要新增搜索/服务。证据 states-s06-03.txt（加载状态需自行检查，不因标题仍3就臆断加载失败）。

## EA25 新训练安排的器械/时长不能沿用旧样例

真实从发现→一周两练→选择B→替换确认→开始：正确进入高位下拉，但动作说明仍“哑铃·平凳”；今天显示B为32分钟，内容详情原40分钟，所有动作的器械都变成哑铃·平凳。本次器械、时长、休息与历史参考应跟随所选内容/训练日/动作快照，未知不借旧值。可复用既有素材，但不要把高位下拉说成用哑铃平凳。证据 start-day-b-matches-selected-exercises.txt、day-b-today-context-metadata-mismatch.txt、selected-day-b-retained-reload.txt。

## EA26 私有AI草稿生效与公开投稿分开

实际应用候选后S11写“保存并提交审阅后才生效”，把个人草稿的保存/安排与公开分享审核混在一起。既定F06仅改草稿、不自动改当前训练；F07公开审阅是另一个用户选择。改为准确说明已改草稿、如何保存/安排；不能要求用户公开自己的计划才能使用自己的调整。保留固定候选来源、只应用一次、版本/hash失效及教练计划转请教练的边界。证据 ai-candidate-applied-once-draft-only.txt、ai-applied-reload-dedup.txt。

## EA27 完成的草稿工作不能一直显示“待保存”

周岚真实保存并发布5，S15显示已交付5/无未保存改动，回学员列表仍“待处理：计划草稿待保存”。真实已保存/交付/采用结果应更新对应的未完成工作/待办原因与计数；只处理已明确完成的同对象事项，不自动替教练把需要判断的回复/授权事项完成。核对再次编辑时恢复正确草稿状态、取消不完成、失败不完成。证据 zhou-delivered-current-plan-unchanged.txt 与 fresh-revocation-student-list-and-zhou-delivery.txt。

## VR09 扩展复查：所有新保存时间使用应用本地时区

消息已修好，但本轮18:19实际保存的我的内容v4显示10:19；18:20自评显示10:20。MyPlanView/DietView仍直接slice ISO。统一使用格式工具检查所有可见保存/交付/消息时间，不改UTC持久化与冻结日历。证据 own-content-pending-review-reload.txt、diet-self-assessment-reload-no-intake.txt；Lead时钟2026-10-24 10:20UTC，即本地18:20。

## VR10 撤权确认主语

学员确认框写“你不能替教练恢复授权”，主语反了。健康授权由学员控制，教练不能替学员恢复；普通沟通继续。证据 health-revoke-confirm-dialog.txt/截图。

## VR11 窄屏200字号的内部裁切

180个视图尺寸捕获中，S01 320×900/200字号的相关安排4个按钮超出右边界，根scrollWidth被clip掩盖。看 bounds-audits.jsonl 中 s01-today-320-text200 与截图；不能改为overflow:hidden让检查“通过”。内容详情S03 320/200的图片叠字也顶到图片顶部，请实际看 s03-plan-detail-320-text200.jpg，按真实文字尺寸让素材区增高或把文字移到正常文档流。保留首批高级克制风格、44触达与正常字号构图，复验五宽/100/200及长中文。

## EA06 文案残留

计划发布中状态仍“没有虚假等待；…”，这是实现/验收解释，不是用户需要的内容。删掉此类设计过程话语；有用的结果、范围、失败恢复说明保留。不要把设计理由写进产品。证据 statesc-s15-04.txt。

## 验证和正常交回

先做以上有界修复，逐项给有效正反例；保留已有授权/未读/双方草稿/迁移行为。独立预览状态检查每态先重置样例，避免上一状态污染；不能只看按钮存在。交付变更后按同一源码跑typecheck/build/所有测试并真实浏览器复查；未测写明。

原D01-03、fix01报告与清单保留。新增 D01-03-fix02-report.md/json、独立source/evidence/report SHA清单；记录实际命令退出码、证据、未测、预览PID/URL。报告写完后正常结束并停止源码变动，让Lead冻结；不自行commit/reset/合并、不改根任务状态。全量55项/16族及动效最终接受仍由Lead完成。
