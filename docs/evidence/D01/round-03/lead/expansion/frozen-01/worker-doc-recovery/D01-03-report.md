# D01-03 全量 UI 候选报告（冻结 6dba）

- 状态：`candidate` / `ready_for_lead_independent_review`；**尚未 accepted**。
- 候选提交：`6dba9b6085c9a7f0b2ac84cf0fa8f290d4a7871d`（tree `c8f84ef126e0c140e100600738eb703936fbd349`，分支 `ui/full-round-three`）。历史输入基线为 `b3949bfbd0ec5e5b6d88594c3c3948b47681b8ed`，不回退。
- 工作树/端口：`/private/tmp/nerae-ui-complete`；`http://127.0.0.1:4187/`（仅 127.0.0.1，监听 PID 64273，保持运行，不重启/不停止）。
- 执行：DeepSeek Harness `session-15de3174-0b22-47b7-9574-29dd3b032a8c`；OpenCode Go / `opencode-go/deepseek-v4.1-flash`；reasoning Medium（Lead 选择）。

## 1. 传输中断与恢复事实

上一轮执行在约第 336 步后出现 `TRANSPORT Connection error`，内部 5/5 重试失败，**不是正常完成**。Lead 已将当时已有文件原样冻结到 6dba；本轮恢复**只写 worker 文档**，未改 `design/prototype/**`、未改 Git HEAD、未重置浏览器数据、未重启 4187。以下命令结果来自冻结前同一工作树，冻结后未由执行者重跑。

## 2. 最新同版执行者检查（实际退出码）

| 命令 | 结果 | 日志（sha256） |
|---|---|---|
| `vue-tsc --noEmit` | exit **0** | `logs/d01-03-typecheck-candidate.log`（19eaf438…） |
| `vue-tsc --noEmit && vite build` | exit **0**；产物 `index-cjaV1nDA.css`、`index-CutKKUY-.js` | `logs/d01-03-build-candidate.log`（c46e73db…） |
| `vitest run` | exit **0**；4 files / **75 tests passed** | `logs/d01-03-test-candidate.log`（29a34b1f…） |

这些是**执行者检查**，不是 Lead 冻结版独立验收。报告 JSON 记录了完整命令、退出码和日志散列。

## 3. 55 项映射与 16 族

- `implementation-map.json`：**55/55** 来源项、**16** 族、**F01–F07**、**M01–M12**；逐项列出界面、文件、可操作路径、测试与 `pending_final_frozen_recheck`。
- 文件 sha256：`8a6097fa44335e5f218f45928365579a37b1c86e91c70c8bd11c567e9f9b7ba0`。
- 16 族：S01 今天、S02 发现、S03 计划详情、S04 训练、S05 记录、S06 教练列表、S07 教练详情、S08 消息、S09 收到计划、S10 我的内容、S11 AI 整理、S12 饮食、S13 待办、S14 学员、S15 教练计划、S16 服务。
- 另有 S01 的现用教练计划详情子页 `studentPlan`，与「我的内容」严格分开；不计为第 17 族。

## 4. EA01–16 / VR01–08 处理

| ID | 处理 | 当前 |
|---|---|---|
| EA01 | 去掉按动作数量猜训练日；保留完整训练单元，另手写两周训练日 | fixed，待 Lead 复验 |
| EA02 | 删除重复交付字段；`coach.deliveries` 唯一快照源，收到/消息/版本同源 | fixed |
| EA03 | 当前对象/选择持久化并冷加载恢复 | fixed（模块冷重载）；浏览器冻结版待验 |
| EA04 | 替换归档已确认组，未完成不伪称完成 | fixed |
| EA05 | 会话标题/服务/收件人同源，去掉硬编码人物 | fixed |
| EA06 | 清掉布局理由、重复免责、排除项自述与内部状态枚举 | fixed，文案复验待做 |
| EA07 | 所选训练日进入同一会话；刷新保留；同计划同日幂等 | fixed |
| EA08 | session/archive ID 含 dayId+sequence；12×8 与 17×9 保留 | fixed |
| EA09 | `coachSnapshots`+`activeCoachStudentId`；周岚/林澈计划、证据、发布隔离 | fixed |
| EA10 | 未授权/结束关系隐藏健康事实；计划与服务保留 | fixed |
| EA11 | 公开预览含组数/目标/组间休息/非空公开备注；健康备注排除；提交=待审核 | fixed |
| EA12 | 匿名→本地登录→年龄；取消不建训练；未成年停止服务可浏览；未知年龄不能换内容绕过 | fixed（仅本地 UI 状态，不接真实登录/短信） |
| EA13 | 保存同步 summary/scope/goals/response/price/unit/cycle；加载用已保存 services 重建公开投影；旧约定快照独立；只改变化包版本 | fixed，含 projection follow-up |
| EA14 | 保留原始价格输入；0–9999 整数校验；负数/空/非法就近报错并禁保存；0 可用 | fixed |
| EA15 | 有未来安排/已完成会话时先替换确认；取消不改版本/未来/历史 | fixed |
| EA16 | 旧 services 按包补字段；priceInput 由 priceYuan 转换；不删存储/不换 key；旧约定按服务版本恢复 | fixed，含 projection follow-up |
| VR01 | 触达 ≥44；390/320 共 18 屏审计 0 个小目标 | fixed（仅实测宽度） |
| VR02 | 今天「现用安排」→ StudentPlan；独立「我的内容」→ MyPlan | fixed |
| VR03 | 消息去规则自述；服务/授权折叠；当前服务与既有约定分列 | fixed |
| VR04 | 动作归一；图表按真实日期间距、kg/秒刻度；未练明示 | fixed |
| VR05 | 当前 199/次 与既有 699/月·4周 分开；查看服务带回同包 | fixed |
| VR06 | 移除布局解释与重复免责；AI ready 改为可用帮助；饮食自评前移并收拢边界 | fixed |
| VR07 | 预览/详情同源含计价单位+周期；本地时间格式；switch「学员可见」与 checked 同义 | fixed |
| VR08 | 动作条改为内容流末尾静态块；390×900 动作条 top 1446、首条正文 749–805，不遮挡；320 同样 | fixed（390/320 实测） |

未发现仍可复现的 24 项代码缺陷；但 **Lead 独立验收未完成**，下一轮仍可能新增问题，不能把本表当作 accepted。

## 5. 实际浏览器 / 模块替身 / 未测

**实际浏览器（Chrome for Testing + CDP）**：58 张截图，覆盖 320/390/430/768/1440；390×900 与 320×900 的 18 屏可见控件 bounds 审计均 0 个 <44；VR08 用真实 DOM rect 验证不遮挡；分享审阅、S16 保存投影、周岚计划/详情、匿名/未成年准入、消息咨询上下文有 390 实际截图；200% 文字有记录/我的内容截图。
**模块替身**：vitest/jsdom、固定样例与 localStorage；包括旧 services 缺字段冷加载、旧约定恢复、价格校验、替换确认、按学员隔离、75 项回归。jsdom 挂载 App 不等同真实浏览器。
**未测/未验证**：Lead 冻结版五宽度/200% 全屏/动效验收；430/768/1440 逐控件 bounds；200% 全部 18 屏；投影修复后的真实浏览器旧 localStorage 冷重载（仅有 jsdom+store 回归）；真实登录/短信/年龄/健康鉴权/服务端权限；Go API/DB/OpenAPI；微信/uni-app/真机/软键盘/safe-area/系统字体；真实 AI/消息/支付；用户研究与专业审核。

## 6. 散列与限制

- 源码：以 6dba 为准，60 个 `design/prototype` 文件清单 `logs/d01-03-source-sha256-frozen.txt`，清单 sha256 `02cc9625cdab20460c7dd054d2cadfa7a918026fe09e63eb114e039cf7ff01da`。关键文件：`store.ts` bc9abfae…、`App.vue` 30e31362…、`IncomingPlanView.vue` 44391465…、`CoachServicesView.vue` c18e3a6e…、`MyPlanView.vue` 79c7f3b1…、`base.css` e4d17164…。
- 证据：`logs/d01-03-evidence-sha256-frozen.txt`，清单 sha256 `69bf71d02a691d27743ace7244fe200adf0eca34bc80413952038dfb81331d13`；含 58 截图、命令日志、bounds、implementation-map、progress。**持续写入的 dev 日志未计入**，避免冒充不可变证据。
- 限制：固定样例、本地预览；不构成业务鉴权/健康/服务/生产结论；全页截图中的固定底栏重叠不是实际遮挡，VR08 以 DOM 几何为准。

## 7. 交回

- 报告：`D01-03-report.md`、`D01-03-report.json`；映射：`implementation-map.json`；进度：`D01-03-progress.md`；历史 `FIX_01.md`/`FIX_02.md`/`fix02-report.json` 保留。
- 4187 保持运行；源码保持 6dba 不再变化。请 Lead 冻结该候选并独立跑代码/身份/流程/视觉/动效检查，再决定接受或给下一轮修复。
