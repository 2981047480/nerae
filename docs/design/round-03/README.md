# 第三轮设计与 UI 交付

用户已授权逐项推导、形成开发文稿并交指定 DeepSeek 实现。入口为 [STATUS](STATUS.md)，当前接受情况以实际证据为准，不能把文稿覆盖当 UI 完成。

| 顺序 | 文件 | 用途 |
|---|---|---|
| 0 | [任务卡](00_CONTEXT_AND_SCOPE.md) | 目标、责任、真源、范围与接受 |
| 1 | [设计意图](01_INTENT_TO_EXPERIENCE.md) | G01–G07、N01–N03、L01–L09 |
| 2 | [情境与交互](02_SCENARIOS_AND_INTERACTIONS.md) | C01–C08、责任分工、恢复、动作预算 |
| 3 | [视觉与动效](03_VISUAL_AND_MOTION.md) | B01–B05、V01–V08、M01–M12 |
| 4 | [取舍与验证](04_TRADEOFFS_AND_VALIDATION.md) | T01–T06、H01–H05、Q01–Q04 |
| 5 | [UI实施规格](05_UI_BUILD_SPEC.md) | S01–S16、内容、数据与实现边界 |
| 6 | [执行者文稿](06_EXECUTION_PACKET.md) | 指定模型、读取次序、写域、批次与报告 |
| 7 | [主代理验收](07_ACCEPTANCE.md) | 高级感、美观、动效、状态与完成审计 |
| 8 | [覆盖表](COVERAGE.md) / [数据](coverage.json) | 55个设计项的来源与层级追踪 |

结构检查：`api/tests/.venv-py311/bin/python scripts/design/check_round03.py --stage handoff`。`--stage ui` 在真实 UI 接受前应失败，不因结构检查通过而自动接受视觉。
