# R05 验证 · 2026-10-27

状态：本地实现与以下检查通过，等待用户判断视觉。Owner：当前 Lead / 宿主 Codex，串行执行，无子代理。Git 基线 e4610a7；本次结果是未提交工作树，不是新接受提交、正式业务实现或发布。

## 实际改动

- `TodayView`：本次记录与教练已有点评并置；手机先呈现跟进，再呈现周历。历史次数/组数从原明细汇总，下方连接饮食、免费计划和完整内容。
- `ExploreView` / `ContentCard`：摄影与具体内容组织训练、饮食和服务；收藏、搜索和计划跳转复用原状态。教练卡报价取展示交付的同一服务包，避免拿另一低价包作起价。
- `CoachDetailView`：提供者、交付、服务选择、报价、样例与咨询形成连续路径。选单次服务时清单和咨询报价一起改变，原月度约定快照保留。
- `RecordsView`：同一范围的次数、实际组数和选定动作最高值；图表继续使用原始确认记录和单位。`PlanDetailView` 衔接内容图片与标题，不改变采用规则。
- `palette.ts` / `round-five.css` / `PreviewControl`：单一墨石/瓷白/熔橙候选，移除旧三候选入口；外观与训练数据分开。`PhotoFrame` 在图片源改变后重新尝试加载。
- `store.ts`、样例数据、API/ER、依赖与锁文件本轮未改；Git 中这些文件的既有改动属于 R04。两种记录方式、已有输入和无计时约束继续生效。

## 可复查检查

在 `design/prototype` 中执行：

| 命令 | 结果与证据 |
|---|---|
| `/opt/homebrew/bin/node node_modules/vue-tsc/bin/vue-tsc.js --noEmit` | 退出 0，[日志](evidence/typecheck.log) |
| `/opt/homebrew/bin/node node_modules/vitest/vitest.mjs run --reporter=dot` | 退出 0，6 文件 / 137 测试，[日志](evidence/vitest.log) |
| `/opt/homebrew/bin/node node_modules/vite/bin/vite.js build` | 退出 0，[日志](evidence/build.log) |
| `git diff --check`（项目根） | 退出 0 |

新增 6 项接缝测试覆盖：首页进入同一记录及输入/组 ID、点评作者归属、服务切换到咨询、收藏与打开互不干扰、列表交付与报价一致、图片失败后换源恢复。既有 jsdom `scrollTo` 未实现提示仍在日志中，未使测试失败。

IAB 实际使用 `http://127.0.0.1:4197/`。桌面 CSS 视口 1280×720；手机覆盖 390×844，含滚动条时内容宽 375。编码尺寸单独写入 manifest，不用文件名或 CSS 尺寸冒充截图尺寸。

- 实际查看首页、发现、教练列表、服务详情、记录、计划详情、逐组记录的手机/桌面截图，见 [完整首页](evidence/today-desktop-full.jpg)、[手机首页](evidence/today-mobile.jpg)、[服务列表](evidence/coaches-desktop.jpg)、[手机服务](evidence/coach-detail-mobile.jpg)、[记录](evidence/records-desktop.jpg)、[计划详情](evidence/plan-detail-mobile.jpg)。摄影为原有授权内容示意，参考截图未进入产品。
- 训练后 → 每组完成后 → 训练后，14 个组 ID 与所有可见输入完全一致；没有保存、删除或重置原记录。见 [模式检查](evidence/recording-mode-check.json)。
- 搜索不存在内容 → 查看全部恢复；预览加载失败 → 重试恢复六份训练内容；记录无记录状态有出口。没有实际 HTTP 服务，不把预览失败状态说成真实网络测试。
- 手机首页、发现、教练详情的 200% 文字，实际查看并检查控件边界。16 次 DOM 抽查中没有横向边界异常或重复 ID；详情见 [检查](evidence/browser-checks.json)。懒加载的离屏图片可暂未加载，完整首页滚动捕获时图片已加载；不将离屏懒加载当故障。
- 从实际首页 DOM 抽取 12 组文字/底色，最低对比度 5.09，见 [读数](evidence/contrast-samples.json)。仅为抽查，不代表所有元素、照片叠字或完整无障碍认证。
- 最后读取捕获到的浏览器 error 为空，见 [错误记录](evidence/browser-errors.json)。检查后恢复 100% 文字、正常样例状态和默认桌面视口，停留新版首页。

## 证据与限制

Appllama 实际读取 MasterClass 首页/课程详情与 Airbnb 服务发现/服务详情/菜单五屏，原图、screen ref 与哈希见 [参考清单](references/manifest.json)。只借鉴内容主题、提供者与具体交付/报价的组织方法，没有复制图片、支付或评分。

[manifest.json](manifest.json) 绑定当前源文件、构建及最终截图字节。`before-today.jpg` 为改前对照；`browser-captures.json` 是捕获过程读数，同名重拍以最终文件哈希为准。原项目同步前确认其源仍与 R04 manifest 一致，避免覆盖后来修改；原目录的独有技能文件、旧证据和 Git 元数据保留。

这次让已有服务与内容更可见，没有完成正式内容生产、真实教练入驻或服务履约。未验证付费意愿、转化、微信真机、HTTP/DB、供应商与生产部署；视觉是否接受由用户判断。R04 契约集成缺口与上线前置事项继续保留。
