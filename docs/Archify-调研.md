# Archify 调研：把想法 / 代码库变成可交互 HTML 图的 Agent Skill

调查日期：2026-10-22　对象：[tt-a1i/archify](https://github.com/tt-a1i/archify) v3.2.0（main@25cf370，2026-10-19）　方法：读源码 + 本地跑 CLI（Node 26.0.0，macOS）+ GitHub API / 官网 / 媒体交叉核实　详细笔记：`.research/archify/code-notes.md`、`.research/archify/ecosystem-notes.md`

## 结论先说

1. **它是什么**：不是画图 SaaS，也不是"Mermaid 主题"。它是「Agent Skill 契约 + 零运行时依赖的 Node CLI + 自包含 HTML 产物」三层：模型只负责把自然语言或仓库证据写成**受 JSON Schema 约束的 IR**，渲染、校验、交付全部由确定性代码完成 [1][3]。
2. **差异化在哪**：交付前的**分层校验**（schema / 布局 / HTML-SVG / 构图指标 / 真实浏览器）与**机器可读修复回执**（稳定诊断码 + 精确 subject + 允许的修复方式），是 Mermaid、Excalidraw、draw.io、Napkin 等都不做的；这让"AI 画得快但画得不对"第一次有了工程化兜底 [3][5][16]。
3. **对 Nerae 值不值得用**：值得，但分两层。**工具层**可直接把 PRD 里的 Mermaid 图、三条核心循环、系统架构转成可交互 HTML（评审/汇报/发群），单文件、可离线；**方法层**更值钱——它的"结构化草稿 → 规则校验 → 人工确认 → 可审计交付"与 PRD §6 的三重闸门、安全状态机高度同构，可当 AI 发布助手/教练副驾的参考实现 [2][3][6]。
4. **但别把它当生产依赖**：单人维护（bus factor = 1）、版本迭代快且已发生过一次 major 契约变更、`finalize` 依赖本机 Chrome、且"校验通过 ≠ 视觉正确"。适合做创作工具与设计参考，不适合做线上 API/SLA 依赖 [5][6][8]。

## 一、形态与定位

| 层 | 事实 | 来源 |
|---|---|---|
| Skill 契约 | `SKILL.md`（137 行）规定 Agent 的工作流：选类型 → 读 schema/示例 → 写 `candidate.json` → 跑 `finalize` → 按回执修复 → 交付链接 | [3] |
| CLI | 纯 Node ESM，**零运行时依赖**（ajv 校验器预编译成 1 MB 的生成物），`engines.node >= 18`；17 个子命令：`render / deliver / finalize / validate / preview / check / browser-check / visual-check / compare / guide / doctor / demo / inspect / patch / migrate / brands / examples` | [3][10] |
| 产物 | 单个自包含 HTML，内嵌字体/样式/脚本/图片；实测一张时序图 821,147 B，除 SVG 命名空间与许可证链接外无外部请求 | [16] |

**能产出什么**：10 种图（architecture、workflow、sequence、dataflow、lifecycle、erd、class、tree、timeline、waterfall）+ `explainer`（把一段回答做成一页"结论先行"的图文答案）。它不是第 11 种图，而是 Markdown 草稿编译成带面板、图表、引用与交互图的页面 [1][3]。

**和 Mermaid 的本质差别**：Mermaid 是"文本 DSL → 静态 SVG"，布局与语义错误基本靠人看；Archify 不解析 Mermaid（明确不做），而是让 Agent 把 Mermaid 当"输入方言"改写成 typed JSON，再由渲染器 + 校验器兜底；复杂图不会被自动布局摊平，因为**坐标是 Agent 判断出来的**——项目自己在 ROADMAP 里承认"自动布局实验 FAILED，布局判断就是产品" [5][7]。

## 二、关键事实（截至 2026-10-22）

| 项 | 数值 | 口径注 |
|---|---|---|
| 建仓 / 许可 / 版本 | 2026-04-15 / MIT / v3.2.0（CHANGELOG 10-18，Release 10-19） | [1][4] |
| Stars / Forks / Watchers | 87,267 / 5,877 / 225 | API 实测；同日 README 徽章仍写 87k |
| Open issue / PR | **140 个 issue + 141 个 open PR**（API 的 `open_issues_count=281` 把 PR 也算进去了） | 这是最容易被误读的数字 [8][15] |
| 安装量 | skills.sh 累计 **168.1K**；zip 附件累计下载 28,361；npm 只有 DSH 插件（周 6,997 / 月 33,078） | README 徽章"130K+"已过时；**累计安装 ≠ 活跃留存**，npm 周下载只覆盖 DSH 一个宿主 [9][11][12] |
| 维护结构 | 93 contributors / 1,741 commits；tt-a1i 一人 **64.7%**，Top5 83.1%，37 人仅 1 次提交 | **bus factor = 1** [8] |
| 发布节奏 | 23 个 Release；v2.16.0 → v3.0.0 仅 29 天；zip 从 15.9 KB 涨到 5.27 MB | 契约断裂风险真实存在 [7] |
| Star 曲线 | **83%（72,204 个）star 来自 2026-08-23 之后的 61 天**；8/27 单日 +5,204；日均从 2,612 衰减到 559 | 由 Trendshift 逐日表反推，`[估算]`，与 API 总值、量子位两篇报道三方吻合 [8][13][14][17] |
| 媒体与榜单 | 量子位 9/1 报道 3.14 万 star、9/13 专访 5.88 万；Trendshift 记录全语言**日榜 #1 三连（8/27–29）** | "GitHub 周榜第一"目前只有作者 X 帖 + 媒体转述，未独立验证 [13][14][17] |
| 作者 | 涂少坤，小红书 AI Native 工程师（2026-05 入职）；项目与其"专升本逆袭"叙事高度绑定 | 传播驱动 > 功能驱动，见风险 5 [14] |

## 三、它怎么工作（为什么比"让模型直接吐 SVG"稳）

1. **生成**：Agent 按 schema 写 IR。全部层级 `additionalProperties: false`，拼错字段直接报错而不是静默忽略 [3][10]。
2. **校验**：六层——① JSON Schema（预编译 ajv）② 跨集合语义（重复/悬空 ID）③ 渲染器布局（重叠、文本溢出、箭头过短、ERD 域带）④ 产物 HTML/SVG（11 项，含 `single_svg`、`orthogonal_arrows`、`label_route_clearance`、`relationship_crossings`）⑤ 构图指标（交叉、走廊、弯曲数、桌面可读性）⑥ 真实 Chrome 四视口 × 双主题（投影后节点字号 ≥ 6 px）。全仓库共 **172 个诊断码** [3][10][16]。
3. **质量档位**：`showcase` 是零 warning 的硬档位，`standard` 把构图问题降级为 warning；密集 ERD 允许 standard [3]。
4. **修复回执**：失败输出 `{code, severity, message, subject, evidence, supportedFixes[]}`——subject 精确到 JSON 路径（实测：`/messages/1/to`），supportedFixes 直接列出允许的取值；"最多两轮修复"是 Skill 层的流程约束，代码里没有硬计数器 [3][16]。
5. **交付**：目录锁 + pending journal + provenance sidecar + no-clobber 备份/隔离；失败保留上一版（last-known-good），可用 `recover-output` 显式恢复。文档自己承认这是"可恢复发布"，**不是 crash-atomic** [3][5]。
6. **安全与隐私**：运行时零依赖、无遥测；`preview` 只听 `127.0.0.1` 随机端口；更新检查只 GET 一个固定 manifest（1 s 超时、32 KB 上限，不上报版本/项目/账号），`ARCHIFY_UPDATE_CHECK_DISABLED=1` 可完全关闭；无 `postinstall` 脚本 [3][10][18]。

**按组件粒度已绘制**：[Archify 组件架构](diagrams/archify-architecture.html)（Agent 侧 → IR → 校验/渲染 → 门禁 → 交付）与 [功能地图](diagrams/archify-features.html)（30 节点，生成/校验/查看/交付/集成/工程），源 JSON、回执与预览见 [docs/diagrams/README.md](diagrams/README.md)。

## 四、本地实测记录（2026-10-22，可复现）

| 步骤 | 命令 | 结果 |
|---|---|---|
| 解包 | 下载 v3.2.0 `archify.zip`（5.27 MB） | 200 个文件 / 17.8 MB |
| 自检 | `node bin/archify.mjs doctor` | 27 项全 `[ok]`（11 个渲染器、校验器、预览、交付运行时） |
| 选型 | `archify guide "Show an API request with Redis cache miss" --json` | 推荐 `sequence`，给出 include/avoid 与提示词 |
| 校验 | `validate sequence examples/cache-miss-request.sequence.json --quality showcase --json` | 0.67 s，9 项检查全过 |
| 交付 | `finalize sequence ... out/cache-miss.html --quality showcase --json` | validate/deliver/check **pass**，browser-check **skipped**（本机无 Chrome/Chromium/Edge）→ **exit 2**；HTML 已生成但按规则不算交付成功 |
| 故障回执 | 把消息 `to` 改成不存在的节点再 validate | exit 1，`sequence/unknown-endpoint`，subject `{path:"/messages/1/to"}`，supportedFixes 列出 3 个合法节点 ID |

**实测结论**：本地评估成本很低（< 5 分钟）；但 **native diagram 的 `finalize` 必须有 Chromium 系浏览器**（Safari 不算，可用 `ARCHIFY_CHROME` 指定），否则会在最后一道门 fail-closed——这既是优点（不假装通过），也是部署门槛。应急可先 `validate` + `deliver` 出 HTML，但不要对外宣称"已通过检查" [16]。

## 五、工程与生态信号（哪些值得信）

- **测试**：`test/` 314 个文件 / 4.56 MB；但 `npm test` 只跑一个 core-smoke（11 个示例的冒烟），全量在 CI：14 个 job + release 门禁（3 OS 安装/升级、zip 与重建逐字节一致、生成物漂移检查、WebM 真解码）[5][10]。
- **Benchmark 反而很克制**：`ordinary-model-floor` 公开"普通模型首次可用 10/15"（MiniMax 4/5、Qwen 3/5、DeepSeek 3/5），后两轮都是 8/15 并明写"**没有看到 uplift**"；`repair-rounds` 明确是合成缺陷注入、不代表 Agent 行为；`real-repo-generation` 标注"尚无结果" [5][10]。
- **依赖与许可干净**：运行时零依赖；JetBrains Mono（OFL）、Simple Icons（逐图标许可表）、Pele 1.3.0（MIT，仅构建期用）都有 NOTICE [10][19]。
- **反差面**：ROADMAP 记录失败实验与否决项（不自动布局、不解析 Mermaid、不做 WYSIWYG/PDF 按钮/移动端），这种"把不做写清楚"的做法少见；同时也有文档漂移（schema README 仍按 5 种图组织，实际 12 个 schema）、"原子交付"用词强于实现 [5][10]。

## 六、风险与局限（采用前逐条确认）

1. **bus factor = 1**：一人 64.7% 提交、Top5 83.1%，无公开 maintainer 团队/治理文件；140 open issue + 141 open PR 对单人维护是实质负担 [8][15]。
2. **契约会变**：6 个月 23 个 release、已发生 v2→v3 major；钉 `latest` 的集成会碎，必须 pin 具体版本 [7]。
3. **finalize 依赖 Chrome**（本次实测），无浏览器环境只能降级使用 [16]。
4. **校验通过 ≠ 视觉正确**：issue #248 中连线渲染错误但交付 9/9 通过；#1025/#1020 暴露校验器在边界输入上的截断 [5][15]。
5. **热度 ≠ 留存**：83% star 来自 61 天，日均新增已从 2,612 衰减到 559；16.8 万 install 是累计值 [8][17]。
6. **token / 时延成本不低**：量子位两篇都提到"消耗额度大"，issue #22「太费 token」、#66「很慢」[13][14][15]。
7. **单文件巨石**：`archify.mjs` 7,289 行、`visual-check.mjs` 2,779 行，另有 1 MB 生成校验器与 787 KB 模板；改动集中在少数巨石文件 [10]。
8. **隐私风险可控但非零**：更新检查默认联网（可关）；DSH 插件只承诺 `@deepseek-ai/dsh@0.1.2-rc.1`，桌面端未验证，且其 `!!js` patch 在宿主进程求值 [18]。
9. **明确不支持**：通用自动布局、Mermaid 解析、WYSIWYG 编辑、托管分享、独立移动端、RTL 布局 [1][5][10]。
10. **内容边界**：Timeline/Waterfall 的示例时间是示意数据，不是实测；图也不证明线上真实运行状态 [1][7]。

## 七、对 Nerae 怎么用

### 7.1 直接可用（1–2 天，产出可分享文件）

| 场景 | 图类型 | 说明 |
|---|---|---|
| PRD §7 数据模型（20+ 表 ERD） | `erd` | 用 `standard` 档（showcase 对密集图过严）；点表/列看详情，比 Mermaid 静态图适合评审 |
| PRD §8.1 信息架构 | `tree` | 学员端/教练端/后台三层，单父节点层次正好 |
| PRD §2.4 三条核心循环 + §5.8 预约与服务关系 | `lifecycle` / `sequence` | 把"预约 → 平台外成交 → 服务关系确认 → 交付 → 续费"画成状态机/时序，教练与运营看得懂 |
| `docs/系统架构与技术选型.md` 的逻辑/部署架构、AI 计划生成流程 | `architecture` / `sequence` | Go + uni-app + 腾讯云 + MiMo 的分层与调用链 |
| 市场事实清单 / 方案复核 / 本调研 | `explainer` | 结论先行的一页答案，带编号来源，适合给投资人或团队同步 |

安装：本地 `npx skills add tt-a1i/archify -g`（或 DSH 社区插件 `@tt-a1i/archify-dsh@1.0.0`）；产物是单 HTML，可直接发微信/飞书/邮件，离线可看 [1][18]。

**已生成第一版（2026-10-22，v3.2.0 全门禁通过）**：[数据模型 ERD](diagrams/nerae-erd.html)（15 表节选，standard 档）与 [信息架构](diagrams/nerae-ia.html)（37 节点，showcase 档）；源 JSON、回执、预览图与重生成命令见 [docs/diagrams/README.md](diagrams/README.md)。

### 7.2 方法论借鉴（比工具本身更值钱，对照 PRD §6）

| Archify 的机制 | 可映射到 Nerae 的 AI 设计 |
|---|---|
| typed JSON IR + 严格 schema（拼错即报错） | §6.2「强约束 JSON schema + 动作白名单」 |
| 六层校验 + showcase 零 warning | §6.3 三重闸门（动作白名单 / 规则校验 / 人工确认） |
| `diagnostics{subject, evidence, supportedFixes}` | 学员侧"建议调整"必须给出可执行的 diff 与备选，而不是一句"再试试" |
| finalize 首个失败门禁即停、不降级 | §6.3「安全阻断不得换入口/降级模板/改走问答绕过」 |
| `gates` + `visualReview: not-requested`，区分"自动通过/人看过" | §6.4 状态机与「AI 生成 · 教练已审核」标识、审核记录 |
| lock + journal + no-clobber + last-known-good | 草稿 → 确认 → 发布；失败保留上一版，不破坏已发布内容 |
| 172 个诊断码 + "失败给修复回执" | 后台 AI 监控（§5.11）按错误类型聚合，而不是只看成功率 |

最值得抄的三条：**① 让模型只填结构，确定性规则放工具层；② 失败返回"定位 + 证据 + 允许的修复"，而不是重试；③ 交付做成可恢复、可审计，并把"自动通过"与"人真的看过"分开报告。**

### 7.3 不建议的用法

- **不要做线上功能依赖 / 生产 API**：它是创作工具，无 npm 包、无稳定 API、无 SLA，单人维护 [7][8]。
- 不做协作画布、移动端查看、WYSIWYG 精修、营销级视觉图——这些用 Excalidraw/draw.io/Whimsical/Napkin 更合适 [6]。
- 内网/合规环境必须设 `ARCHIFY_UPDATE_CHECK_DISABLED=1` 并 pin 版本；CI 缓存注意仓库 881 MB、包 17.8 MB [1][18]。

### 7.4 落地门槛与验收

- 环境：Node ≥ 18 + **Chrome/Chromium**（finalize 必需）；pin v3.2.0。
- 验收 3 个指标：生成一次的人工修正次数、单次耗时/token、图上节点能否点回真实文件。
- 先做**非关键路径**（内部评审图、架构图），跑 3 张真实图后再决定是否进 PR 评审流程；不要替换现有文档/画图流程。
- 本调研不改变 A 方案冻结结论，也不影响 PRD 里程碑。

## 八、结论

- **值得用**：作为"把 PRD/调研/架构变成可交互沟通物"的创作工具，以及 AI 草稿管线的参考实现；成本低、产物可分发、失败可诊断。
- **不要依赖**：不作为线上运行时、协作平台或需要 SLA 的组件；不要把 87k star 当作留存与质量证明。
- **什么时候再看**：v3.x 契约稳定（schema/CLI 不再 breaking）、explainer 与 DSH 集成走出"社区插件/pin 死 rc 版本"阶段后，再评估团队级采用。

> 口径注：star 曲线、8/22 基数、日均值均为 Trendshift 逐日表反推的`[估算]`；"周榜第一"仅作者自述+媒体转述；安装量是累计计数；issue 处理速度用"关闭时长"代理，不等于修复时长。以上事实截至 2026-10-22，项目迭代快，采用前请复核当前版本。

## 来源

1. GitHub 仓库（README、版本、许可、scope） https://github.com/tt-a1i/archify
2. PRD（AI 设计规范 §6、数据模型 §7、信息架构 §8、核心循环 §2.4） [docs/PRD.md](PRD.md)
3. Skill 契约与 CLI 文档 https://github.com/tt-a1i/archify/blob/main/archify/SKILL.md
4. CHANGELOG https://github.com/tt-a1i/archify/blob/main/CHANGELOG.md
5. ROADMAP（失败实验、Not planned、布局判断） https://github.com/tt-a1i/archify/blob/main/ROADMAP.md
6. 竞品对比与生态笔记 `.research/archify/ecosystem-notes.md`（含 Mermaid/Excalidraw/draw.io/Eraser/Napkin/Whimsical/tldraw 官方链接与价格快照）
7. Releases API https://api.github.com/repos/tt-a1i/archify/releases?per_page=100
8. 仓库 API https://api.github.com/repos/tt-a1i/archify
9. skills.sh 安装页 https://skills.sh/tt-a1i/archify/archify
10. 代码级笔记 `.research/archify/code-notes.md`（文件行号、schema、诊断码、CI、安全检查）
11. npm registry https://registry.npmjs.org/@tt-a1i/archify-dsh
12. npm 下载量 https://api.npmjs.org/downloads/point/last-week/@tt-a1i/archify-dsh
13. 量子位 2026-09-01《GitHub最热架构图 Agent，开发者故事看哭了》 https://www.qbitai.com/2026/09/482469.html
14. 量子位 2026-09-13 专访 https://www.qbitai.com/2026/09/488519.html
15. Issue 抽样：#248（校验通过但视觉错误）、#22/#66（token/慢）、#1012（Windows 交付） https://github.com/tt-a1i/archify/issues
16. 本次本地实测（命令与输出见 §四；快照 v3.2.0 / Node 26.0.0）
17. Trendshift 逐日 star 数据 https://trendshift.io/repositories/31352
18. DeepSeek Harness 集成说明 https://github.com/tt-a1i/archify/blob/main/integrations/deepseek-harness/README.md
19. 第三方许可清单 https://github.com/tt-a1i/archify/blob/main/THIRD_PARTY_NOTICES.md

## 未验证 / 留白

- "GitHub Trending 周榜第一"的独立证据（日榜三连冠有 Trendshift 可查；周榜只有作者 X 帖截图与媒体转述）。
- X 上"多条 10 万赞测评"的互动量；Discord/QQ/微信群规模。
- issue 首次人工响应时长（只用关闭时长作粗代理）；Socket 审计 Warn 的具体告警内容。
- `ordinary-model-floor` 的 10/15、8/15 未复跑；`real-repo-generation` 尚无结果。
- v3.2.0 tag 与 2026-10-19 main 的差异（本地快照非最新 `pushed_at`）；DSH Desktop 0.2.0-rc.2 与 Windows 行为。
