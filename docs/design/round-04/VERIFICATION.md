# R04 本地样机验收 · 2026-10-27

后续色彩迭代另见 [palettes/README.md](palettes/README.md)。本文件及 evidence/ 的源码/构建清单保留初次重做时的版本，不作为后续配色源码的哈希证明。

结论：本地交互样机的实现与下列检查通过，交付用户评估视觉。没有将 D01 或 U01–U07 改成 accepted，没有发布业务应用。代码基线 e4610a7；最终源码和构建文件的逐文件 SHA256 见 evidence/source-manifest.json、evidence/build-manifest.json。

## 实际运行结果

工作目录：/Users/zephyr/.codex/worktrees/nerae-recording-redesign/nerae/design/prototype。

| 检查 | 实际结果 | 证据 |
|---|---|---|
| node node_modules/vue-tsc/bin/vue-tsc.js --noEmit | 退出 0 | 本轮终端执行；无诊断输出 |
| node node_modules/vitest/vitest.mjs run --reporter=dot | 退出 0；5 文件、131 测试通过 | [vitest.log](evidence/vitest.log) |
| node node_modules/vite/bin/vite.js build | 退出 0；构建生成 dist | [build.log](evidence/build.log)、构建 SHA256 清单 |
| Python run_contract_tests.py --summary | 退出 0；72 静态契约方法通过 | [contract-summary.json](evidence/contract-summary.json) |
| Python scripts/plan.py check | 退出 0；valid=true，37 工作包、25 执行卡、5 历史 accepted；ready=F01-01/L01-01 | 本轮终端执行；任务状态没有升级 |
| git diff --check | 退出 0 | 本轮终端执行 |

实际 Node 为 /opt/homebrew/bin/node。契约检查使用原项目已有 api/tests/.venv-py311/bin/python，在本隔离树运行检查脚本；不是在旧目录检查新代码。Vitest 日志含既有 jsdom scrollTo 未实现提示，未造成失败；实际页面滚动另用浏览器检查。

## 记录逻辑与回归

src/tests/recording.test.ts 的 16 项针对本轮行为：
- 默认训练后填写；历史数据仅供参考，明确沿用才填入。
- 两种模式共用 session/set ID、实际输入和所选动作，切换及恢复不重新造组。
- 空白不计入；半行和无效值阻止保存，先校验再应用，不产生半批假成功。
- 每组完成后任选动作记录；重复确认保留原确认时间，不生成计时器或休息间隔。
- 重复保存/换计划只留同一记录 ID；未确认编辑不覆盖最后已保存事实，新确认组不丢失。
- 增删组/动作、编号与 ID、删除后撤销保值；其他组的后续编辑不会被撤销覆盖。
- 补记、未来/无效日期、已保存日期修改、手填秒数、自重次数。
- localStorage 抛错时明确失败并保留输入；恢复后重试；旧 rest 状态迁移删除。
- 冲突不把参考值当成本机事实，采用另一份后输入与最终保存值一致。
- 空记录有添加入口，空保存报错且不生成记录；真实 Vue 控件填写、切换与逐组确认。

原有权限、身份、版本采用/发布、消息、饮食与教练操作测试也在这次完整测试运行内。没有用静态测试替代真实服务。

## 实际浏览器检查

本机预览 http://127.0.0.1:4197/ 从当前隔离树运行。通过 CUA 的真实控件完成：整次/逐组切换、填写重量次数、添加组、半行保存聚焦错误、逐组确认、刷新恢复、添加俯卧撑并自动采用自重次数、删除确认与撤销、最终保存 6 组。

[DOM smoke](evidence/browser-smoke.json) 从真实页面读取组 ID 和输入：切换及刷新后相同；逐组可见值匹配；无重复 DOM ID；无训练/休息计时按钮；该页面控制台 error 列表为空。未通过隐藏状态写入模拟用户操作。

实际查看 16 张截图，见 [截图清单](screens/manifest.json)。覆盖今天、两种记录方式、无效输入和成功保存、发现、计划详情、记录图表、消息、教练待办、教练审阅。手机设定 390×844，窄屏 320×740，桌面 1280×720；浏览器滚动条与截图接口的输出不同于设定尺寸，清单同时记录 DOM 宽度、DPR 和图像实际像素，未冒称原始设备分辨率。窄屏修正后 scrollWidth=clientWidth=305；常规手机为 375，桌面为 1265，没有靠隐藏横向溢出宣称通过。

200% 字号实际检查了记录页顶部和输入区域；按钮会换行，字段和操作可见。减少动效时，所检控件的 transitionDuration=0s、animationName=none；未宣称 60fps 或真机性能。临时 viewport 与字号设置已恢复，最终预览停在今天，记录偏好为训练后记录。

本轮 55 项受控源文件、文档与证据已同步至原项目并逐项核对字节一致；原项目的 .agents、skills-lock.json、旧研究与历史证据保留。原项目 dist 已重新构建，退出 0，见 [original-build.log](evidence/original-build.log)。4197 预览仍来自本隔离树。

## 明确边界

- 数据与人物为样例；输入仅保存在当前浏览器。没有真实登录、跨设备同步、HTTP/DB、AI 供应商或微信真机验证。
- 去除训练播放器、组间倒计时、后台训练时钟；计划处方中的建议休息秒数和预计训练时长仍是内容说明，不是计时功能。时长动作只接受手填实际秒数。
- 正式接口对 durationMin、计量类型、删组、补记和已结束记录修订的缺口见 [INTEGRATION](INTEGRATION.md)，尚未关闭。
- 当前视觉由 Lead 按用户最新要求重做；最终审美接受仍由用户反馈，不将工具输出或自己评分当作用户接受。
