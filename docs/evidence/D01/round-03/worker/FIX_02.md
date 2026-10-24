# D01-02 fix02 修复表（仅 REVIEW_02 三项）

- 实际 git HEAD：`19a2aeb`（Lead 证据提交）；被评审源码：`9b16be064e3da9d11b271b0e00769faa1d9d2079`。`bec98f3` 不是当前源码父提交，不再作为本轮元数据。
- 源修改仍在工作树，未提交。只改 `design/prototype/**` 与 worker 证据；未改 Lead 文稿、任务状态、PRD/API/ER、55 项真源，未扩其余 12 族。
- 服务复用 `127.0.0.1:4186`，PID 74496；工具链 `/opt/homebrew/bin/node` v26.0.0。旧 `report.md` / `report.json` / `FIX_01.md` 保留为历史。

| 项 | 原因与修复 | 实际验证 | 剩余限制 |
|---|---|---|---|
| R08 剩余 | 今天进度句仍重复 CTA 的动作/组数，320 下“第 4 组”被拆行。改为进度只显示“已完成 3 / 12 组”；当前动作与组数只在主操作出现一次：“继续 哑铃卧推 · 第 4 组”；组数短语用 nowrap 保护，主按钮保留短屏可达与 M03 标题接续 | `ui.test`：`.hero-progress-text` 精确等于“已完成 3 / 12 组”；CTA 含“哑铃卧推”“第 4 组”，`.hero-cta-nowrap` 包住组数；原有短屏 CTA 与 M03 测试仍通过 | 实际 320 截图由 Lead 复核；本次无新截图 |
| R10e | BaseSheet 原先只判断 `isConnected`/disabled，跨断点后桌面触发隐藏仍被当可恢复。新增共享 `isElementActuallyVisible`：检查 `checkVisibility`（含祖先 display/visibility/opacity）、实时矩形/offsetParent、aria-hidden 与 disabled。发布弹层传入 `returnFocus`：优先同任务可见且未禁用的 `[data-publish-trigger]`，否则状态区；取消、Escape、成功走同一恢复路径，最后兜底 `#main`，不落 BODY | `ui.test` 模拟 1440→390：焦点先在桌面发布按钮，打开弹层后隐藏桌面触发、显示移动触发，取消后 `activeElement` 为移动触发；成功交付焦点到状态区的既有测试仍通过 | 真实浏览器断点变化与 Escape/取消需 Lead 复测；测试用样式隐藏模拟断点 |
| R15a 放大收藏按钮 | 200% 发现页收藏按钮被 flex 压缩到 34.5/43.648px。给 `.bookmark-btn` 加 `flex:0 0 44px`、`min-width/height:44px`，标签可换行而按钮不收缩；`.featured-badge` 允许收窄换行 | 源码规则；既有收藏状态测试仍通过 | 真实 320/390 命中区由 Lead 实拍复核 |
| R15b 跳过导航黑条 | 200% 时 skip 链接高度变大，固定负 top 露出黑条。改为 top:0 + `translateY(-100%)` 按自身高度完整隐藏，键盘 focus 时 `translateY(12px)` 完整显示；`#main` 与焦点样式不变 | 源码规则；#main 唯一/跳转目标测试仍通过 | 真实 200% 首屏与 focus 截图待 Lead |
| R16 教练编辑器 200% 内部超宽 | 320/390 下编辑器 min-content 达 525px，右侧字段/移除被裁。把 200% 单列改为 `minmax(0,1fr)`，对 coach-grid/plan/field/input/review 等链式子项补 `min-width:0` 与 `max-width:100%`，字段单列、可换行；发布弹层“版本 N”用 nowrap 词组，按钮在 200% 允许流式堆叠，不隐藏控件、不缩小文字、不关闭 200% | `ui.test` 新增发布“草稿版本 8 / 版本 6 / 发布版本 8”词组完整断言；36/36 通过 | 真实 320/390 字段与移除操作可见可输入、390 弹层词组不孤行仍须 Lead 实测 |
| R15a 普通字号命中区 | 发现训练/饮食 tab 实际 30.6×46，桌面今天/发现导航 30.6×44。为 `.mode-tab` 与 `.nav-link` 增加 `min-width:44px`（导航用 inline-flex 居中，tab 保持 46 高），不放大字号、不改视觉比例 | 源码规则；既有布局/交互测试保留 | 真实五档宽度命中区由 Lead 复测；高度浮点差不算缺陷 |
| R16 普通 320 编辑器 | 100% 320 下训练日输入/移除/备注实际右边界 323.5，卡片被 min-content 撑宽。把 200% 下的 min-width 修复改为常态：coach-grid/plan-panel/plan-surface/plan-row/row-head/field-grid/field/field-note/input-wrap/review 等链补 `min-width:0`，plan 面板 `width/max-width:100%`；三列字段改用 `minmax(0,…)`，≤380px 单列；不靠 clip 掩盖 | 源码规则；36 项既有回归通过；未新增伪造几何测试 | 真实 320/390 字段/移除/备注边界由 Lead 量测；≤380 单列会增加高度但保持可读 |
| 文字放大预览 | 新增独立预览入口 100%/200%。`data-text-scale` 同步到 `<html>`，根字号 200%，全部 `--text-*` rem token 与正文/控件文字放大，不做整体 transform；Teleport 弹层同样覆盖。窄屏 200% 下 hero 改为图片+流式正文，字段/指标单列，底栏/弹层允许滚动；预览入口在 200% 收为图标但保留可访问名称。持久化在预览状态，重置预览回 100%，与 Reduce Motion 独立 | `store.test`：200% 持久化；Reduce Motion 与文字大小独立；重置回 1/100%。`ui.test`：documentElement 属性 200/100、预览按钮 pressed 状态、重置回 100% | 尚未由 Lead 实拍四情境与弹层 200%；这不是系统动态字体、微信字体设置或真机验证 |

## 回归命令与结果

| 命令 | 退出码 | 证据 |
|---|---|---|
| `vue-tsc --noEmit` | 0 | `logs/typecheck-fix02-final.log` |
| `vite build` | 0 | `logs/build-fix02-final.log`（CSS 61.51 kB、JS 179.07 kB） |
| `vitest run` | 0 | `logs/test-fix02-final.log`（36/36，3 files） |
| `curl 127.0.0.1:4186/` | 0 / HTTP 200 | 原 PID 74496，仅 127.0.0.1 |

新增 5 项回归（未为纯几何新增镜像测试）：R08 进度/CTA 文案、R10e 跨断点焦点、文字放大预览与重置、文字放大持久化/独立于 Reduce Motion、200% 发布版本词组。既有 R01–R13 通过项未删除或放宽。

## 未验证 / 不在范围

- 真实浏览器普通 100% 与 200% 的 320/390 几何（R15a 收藏/tab/nav 命中、R15b 黑条、R16 字段/移除/备注边界与 390 弹层词组）、真实 1440→390 断点焦点与 Escape/成功、四情境与弹层 200% 可读可滚；
- 200% 为预览文字放大，不是系统动态字体/真机；系统 Reduce Motion、软键盘、真实存储失败浏览器注入、真机与专业审核仍未验证；
- `payment` 不在剩余范围：A 阶段不收款、不代收、不分账；本样机不实现支付。
