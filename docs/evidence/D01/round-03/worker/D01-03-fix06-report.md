# D01-03 fix06 候选报告（待 Lead 冻结复验）

状态：**candidate / 尚未 accepted**。基线为 Lead 冻结 `8e5251943ce06ec85f98fcc99769bf019a845f30`；未改 Git HEAD，4186/4188/4189/4190 未触碰。预览 `http://127.0.0.1:4187/`，PID 64273。

## 最新引用

`incomingView` 对**学员本人、点击的最新版本且与 `student.incoming` 同版**返回 live incoming，不再把所有带版本引用合成为不可用快照；`incomingAvailable` 对 `adopted` 返回 false。390 实际：最新7“采用这份安排”enabled；取消保留现用6、今天3组；确认后现用7、`adopted`、今日记录不变；返回再开同引用仍“已采用”，无重复采用按钮。旧7在新8后仍历史不可采用；教练只读、缺失不 fallback、撤权规则保留。

## C03 单位

新增 `durationPerSide/bodyweight/bodyweightPerSide/breath` 窄类型：`parseContentLine` 保留“次呼吸”；自重计次不显示/要求 kg，秒/侧存 seconds，呼吸次数存 reps；负重/负重每侧仍保留 kg/reps 校验。`lastReferenceLabel` 按 kind 保留“次呼吸/秒/侧”，`ensureTrainingConflict` 的 breath、秒每侧本地/远端文案同步。开始新会话清空旧 `ui.trainingErrors`，不增旧组。

390 实际核心：死虫式仅秒数；鸟狗式 10 次/侧无重量输入，两组完成且 `weightKg=null, reps=10`；侧桥 30 秒/侧存 seconds；猫牛式 8 次呼吸无重量、存 reps；负重错误后重新开始秒数会话，旧错误清空且第一组可完成。模块覆盖旧/新单位标签、冲突预览和换场恢复。

## 验证与边界

同版 typecheck **0**、build **0**、vitest **109/109**、exit **0**；日志 `logs/d01-03-fix06-*`。实际浏览器为 CDP + 390 真实页面；模块为 vitest/jsdom，均不等于 Lead 全矩阵。保留 fix04 引用迁移/记录返回、fix05 M02/M12/V04/文案和 M04/M05。未测：Lead 全 16 族/55 项/五宽/动效、真实服务端权限/消息/健康鉴权、微信/uni-app 真机及所有单位组合。旧事实与已确认记录不改写；未知剂量不猜成默认负重。候选未 accepted。

## 散列

源码清单 `logs/d01-03-fix06-source-sha256.txt`，sha256 `89ef5991…`；证据清单 `logs/d01-03-fix06-evidence-sha256.txt`，sha256 `6a05c617…`；dev 日志排除。请 Lead 冻结后最终复验/集成。
