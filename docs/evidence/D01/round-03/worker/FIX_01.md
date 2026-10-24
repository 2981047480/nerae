# D01-02 第一批修复表（R01–R13）

依据 `lead/REVIEW_01.md` / `lead/AUDIT_02.md`，仅改 `design/prototype/**` 与 worker 证据；未改 Lead 文稿、任务源、验收条件或全局配置。真实服务仍为 `127.0.0.1:4186`，PID 74496。以下“验证”区分状态测试与尚未做的真实浏览器复测。

| ID | 原因与修复 | 实际验证 | 未解决/待复测 |
|---|---|---|---|
| R01 | `validateSetInput` 用浮点乘 100 比较，1.1/2.3/8.2 被误判；改为字符串小数位检查，保留 1–2 位合法小数，拦截负数/非数/三位小数 | `logic.test` 正反例：1.1/2.3/8.2/8.25 通过；12.345、-1.5、abc 拦截 | 无；浏览器输入复测由 Lead 做 |
| R02 | 休息反馈按字符串排序 `completedAt`，新增 UTC 时间排在样例 +08:00 之前；改为按 `Date.parse`。新增记录改用固定样例时间线（最后一个事件 + 休息秒数 + 45 秒），不再混入 02:17 墙钟；休息页显示“哑铃卧推 第4组已记录 · 12.5 kg × 10 次” | `ui.test` DOM 断言与日志：`[fix-evidence] rest-feedback {... "completedAt":"2026-10-24T11:14:15.000Z"}`；`store` 断言新时间 > 19:12 | 刷新后一致性由同一持久化数据保证，Lead 可复测 |
| R03 | `persistNow` 修改被 deep watch 的 `updatedAt` 导致自触发；改为写入快照副本，不再改响应式字段；重置提升代次并清理旧防抖 | `store.test` 日志：`idle-persist {"writesAfterDebounce":1,"writesAfterIdle":1}`；重置后保存 screen 为 today | 无 |
| R04 | 只有已交付后才升版，且没有交付快照；改为**每次实际内容变化保存成功即草稿版本 +1**，无改动不增版；发布时保存不可变 `deliveries[{version,content,deliveredAt}]`，学员现用仍为 6 | `store.test` 日志：交付 8、后来草稿 9、学员现用 6、现用首动作 4 组、交付快照 5 组、当前草稿 6 组 | 学员采用流程仍属后续卡，不在本批 |
| R05 | `persistNow` 吞掉 localStorage 异常且保存仍报成功；改为返回布尔并维护 `storageState`。保存失败回滚已保存标记/版本，保留内存编辑；状态区与训练页提供“重试保存” | `store.test` 日志：首次保存 false，`failureNote:"未能保存到本机。请检查浏览器存储后重试。"`；恢复后 true、版本 8 | 真实浏览器存储注入仍需 Lead 复测；已有状态负例 |
| R06 | 审阅关键前后值 12px、移动端摘要与行内差异重复；前后值升至 14px/1.7，备注 16px/1.7，目标与休息 14px；移除行内重复的 before/after，只在顶部完整改动摘要呈现一次 | 样式源码；`ui.test` 覆盖变更模型；无浏览器字号仪器 | 实际渲染字号与行长待 Lead 复测（本轮未伪称已量） |
| R07 | 差异标签固定为“目标次数”；改为“目标次数/时长”，编辑字段已有“目标次数 / 时长”，秒/单侧/重量不混用 | `logic.test` 断言差异标签含 `目标次数/时长`；审阅内容仍完整 | 无 |
| R08 | 短屏 hero 高度不随视口收缩，515×629 时 CTA 落入固定导航；高度改为 `clamp(340px, min(112vw,72dvh),560px)`。CTA 与进度显示“继续 高位下拉 · 第1组”等具体位置；“此刻”重复三项改为器械/休息/上次训练；休息默认说明后自动进入下一组，“提前结束休息”降为次要文字入口 | CSS 公式与源码；状态测试覆盖跨动作当前组 | **实际 515×629 命中测试未做**，必须由 Lead 复测；不声称已通过 |
| R09 | 原生 search 清除 × 与自定义 × 重叠，搜索词三处重复；输入改为 text（仅保留自定义清除），条件摘要与空态不再重复搜索词，“清除筛选”只在有筛选时显示 | `ui.test`：输入 type=text、仅 1 个“清除搜索”、摘要不含查询词 | 无 |
| R10a | 名称输入约 29px、训练日约 34px 且无关联 label；两者补 `label[for]` 关联与 `min-height:44px` 真实命中区，移除按钮 44px | `ui.test` 断言 label 关联；CSS 源码 | 真实触达量待 Lead 复测 |
| R10b | 教练页嵌套 main，训练页 skip 指向不存在 #main；教练内部改 div，训练 main 设 `id="main" tabindex="-1"`，App main 也加 tabindex | `ui.test`：四情境各仅 1 个 main、1 个 #main，跳转目标存在 | 无 |
| R10c | 弹层初始焦点在容器，Shift+Tab 逃到外部；打开即聚焦首个可聚焦项，keydown 处理焦点在容器/外部两种情况，反向从首项到末项 | `ui.test`：初始焦点在 dialog 内，初始 Shift+Tab 仍在弹层，末项 Tab 回首项，关闭回触发按钮 | 无 |
| R10d | 发布后触发按钮禁用，焦点落 BODY；成功交付关闭弹层后把焦点移到 `status-row`（`tabindex=-1`，保留 polite 宣告），BaseSheet 不再聚焦已禁用元素 | `ui.test`：发布成功后 `document.activeElement` 为 status-row | 真实浏览器焦点顺序待 Lead 复测 |
| R11 | 预览 reduced 只作用 App 根，Teleport 弹层与 html smooth 未覆盖；将 `data-motion` 同步到 `<html>`，全局覆盖所有元素/伪元素，并置 `scroll-behavior:auto` | `ui.test`：`documentElement.dataset.motion === 'reduced'`，Teleport dialog 存在时仍为 reduced；关闭后 full | **CSS 过渡 0.001ms/滚动实际行为未在浏览器量**；Lead 复测 |
| R12 | 单一 filters 在切模式时被重置，器械条件丢失/错套；改为 `trainingFilters` 与 `dietFilters` 分别持久化，切换只换 mode，不清条件 | `store.test`：器械训练 2 份 → 饮食 3 份 → 回训练仍 2 份且器械保留 | 无 |
| R13 | Explore/Coach 多根节点无法被 Transition 动画；两个视图各包单根容器；同时以自绘标题接续实现 M03 | `ui.test`：三个页面切换无 `non-element root` 警告；正常模式生成 `.session-focus-ghost`，reduced 不生成 | **实际 Transition 动画、M03 衔接与 260ms 观感待 Lead 复测**；不增加假等待 |
| M03 | 原为普通视图淡入，Lead 未接受；今天 hero 标题 rect 传给 App，进入训练后按目标标题位置做 260ms 位移/缩放/淡出，同时训练空间淡入；动画不拦截点击；reduced 直接呈现 | `ui.test` 正常生成/ reduced 跳过；实际浏览器观感待 Lead | 真实 220–280ms 视觉连续仍待 Lead 判断 |

## 回归与证据

- 复跑：`vue-tsc --noEmit` exit 0；`vite build` exit 0；`vitest run` 31 passed exit 0（`logs/test-fix-final.log`）。
- 具体快照/持久化证据见测试日志中的 `[fix-evidence]`：idle 保存 1 次后空闲仍 1 次；存储失败提示与恢复；交付 8 / 草稿 9 / 现用 6 及三份内容 4→5→6 组；休息反馈指向第 4 组 12.5×10。
- 未做：真实浏览器复测（尤其 R08 515×629、R11 实际过渡/滚动、R06 字号、R10a 触达、R13/M03 动画）、200% 文本、系统 Reduce Motion、真机输入、专业审核。不得以本表或状态测试代替 Lead 实际复测。
