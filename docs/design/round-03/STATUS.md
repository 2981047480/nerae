# 第三轮设计与 UI 交付状态

## 目标与当前阶段

用户已明确恢复设计：逐项处理 DESIGN_FOUNDATION 的全部项目和层级关系，将设计动机、产品行为、交互与视觉决定写成详尽文稿，再交给 deepseek-harness 中 OpenCode 订阅的 deepseek-v4.1-flash 实现；主代理负责设计把关、集成与验收。优先级为高级质感、美观及配套动画。中间设计推理不能出现在产品界面。

当前阶段：D01-02正在指定Harness新会话实现。此前暂停生成的指令由本次目标明确解除；尚未接受新视觉或 UI，未开始业务后端开发。

## 已核实的现场

- 旧 UI 已清理；当前只有设计逻辑文档，没有可沿用的界面或 token。
- 当前目录已建立本地Git与冻结提交，独立工作树为 `/private/tmp/nerae-ui-round-three` / `ui/round-three`。应用托管入口返回 Not a git repository，已使用原生 git worktree 建立实际隔离。
- 本机存在 `/Users/zephyr/.opencode/bin/opencode`；已读取其 `run` 与 `models` 帮助。模型选择需显式传 provider/model，不能沿用全局默认模型。
- 执行入口已核实为 `/Applications/DeepSeek Harness.app`，活跃本地服务为 127.0.0.1:60406；配置 profile 为 desktop，默认 provider `opencode-go` / model `deepseek-v4.1-flash`。原生界面也显示 DeepSeek V4.1 Flash / Max。尚未提交实现任务。
- `opencode models` 实际返回 `opencode-go/deepseek-v4.1-flash`。直接 CLI 仅用于核实订阅型号；实际实现仍交给用户指定的 DeepSeek Harness。
- 目标由当前线程 active goal 持续跟踪，不把文档完成当作 UI 交付完成。

## 文档链与验收边界

全量覆盖分为：G01–G07 产品基因、N01–N03 心理需要、L01–L09 九层、C01–C08 情境、B01–B05 品牌体验、V01–V08 视觉子系统、T01–T06 矛盾、H01–H05 假设、Q01–Q04 验证原则，共 55 项。逐项记录设计动机和具体决定，再形成可执行规范、执行日志与独立验收，不只做五个示例屏。

业务真源继续为 PRD / APP_FLOW / API / ER。新设计文稿将明确区分设计演示与真实业务实现；不把 UI 原型或样例状态当成后端、微信真机、权限和专业审查完成。

## 接续规则

每次推进更新本文件并引用具体产物、命令或活跃执行句柄。执行任务使用稳定会话标识；观察超时不重启任务。没有真实执行、渲染和检查证据，不写通过或完成。

## 文稿冻结与当前证据

- D01-01 文稿已接受，冻结根提交 `9511bab91da0cfc7e9d2a5c3a538dd646299b29c`；55项、16界面族结构检查通过，计划工具29项单测通过，候选9对正文组合对比检查通过。
- UI实现和视觉接受仍为0；整体goal继续active。
- 本地Git已真实建立，缓存 / 虚拟环境 / 密钥模式忽略；本次只为独立UI工作树准备，不宣称F01-01完整工程工具链接受。
- 下一步：创建隔离工作树，以指定Harness新会话执行D01-02四种代表情境，实际渲染后再扩展。

## D01-02 派发准备

- 主目录冻结提交 `f7a78004c0ed0b18b56fa58e8a313b0cf3205c81`。
- 原生工作树：`/private/tmp/nerae-ui-round-three`，分支 `ui/round-three`；git worktree add成功。
- Harness独立工作区已选中，opencode-go组下DeepSeek V4.1 Flash被选中，Max；未混用旧架构审核会话。
- 精确派发消息：`docs/evidence/D01/round-03/dispatch/D01-02-prompt.md`。
- 写入仅限prototype与worker证据，端口4186；第一批四情境之后停下待验。

- 实际会话标题：**Nerae 第三轮 UI 首批实现**。原生界面已显示进行中、停止生成按钮，并正在读取必需文稿；不是仅排队或待派发。模型选择证据已落盘dispatch/model-selector.txt与截图。

- 持久会话ID：`2b3c2fb7-0ddf-4462-be74-885272454974`，来自新工作树对应的.dsh会话目录；后续返工沿用此会话，不因观察超时重建。
