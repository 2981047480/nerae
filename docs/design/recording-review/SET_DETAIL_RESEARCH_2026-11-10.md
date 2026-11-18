# 逐组记录和总重量：资料核查

核查时间：2026-11-10。目标是回答完成训练后应先让用户看到什么；本页为讨论证据，不是正式规格或已实现功能。

- [Hevy 官方动作历史说明](https://help.hevyapp.com/hc/en-us/articles/35382889578135-Exercise-Performance-Tracking-in-Library-Weight-Bodyweight-Cardio-and-Duration-Based-Exercises)：动作 History 可看到每次训练的组、次数、重量，进入某次训练还可看上下文；同页另说明动作表现图可以选择 Session Volume 等指标。这支持明细和汇总各有用途，但文字不能证明二者在训练完成页的排列顺序。
- [StrengthLog 官方历史说明](https://help.strengthlog.com/help-article/retrieve-from-training-log/)：可按动作查看先前训练并取回以前的组。历史明细可帮助选择下次训练的数值；这是所述功能，不意味着 Nerae 需照搬其交互。
- [Strong 官方指标说明](https://help.strongapp.io/article/226-focus-metric)：可以将 Total Volume 作为某个动作的对照指标，与上次训练比较。帮助页更新于 2024 年，未实机验证当前各版本是否一致。
- Appllama：本次查询了 Strong 的 Workout History / Workout Completion 屏幕目录和 StrengthLog 的 Workout Tracking 屏幕目录；目录标签不能证明实际视觉顺序。实际查看了项目已保存的 [Strong 逐组录入截图](/Users/zephyr/Documents/项目/nerae/docs/design/research/2026-10-27-appllama/references/464254577/oth_s9pyg.webp)，可见动作下面按组列出重量和次数。此图是录入页，不是训练完成页，且含计时功能；不把计时或其视觉复制到 Nerae。本次尝试打开 Hevy 官方历史参考图超时；新检索到的 Appllama 完成页图片未实际查看，不能据目录标签推定画面布局。
- [ACSM 2026 文献](https://pmc.ncbi.nlm.nih.gov/articles/PMC12965823/)给出的 volume load 是按负荷、次数等相乘的统计口径。举例两组 12 kg × 12 次、14 kg × 10 次，合计 284 kg·次；这是举例，并非用户数据、单次举起 284 kg，亦非训练效果或肌肉受力的直接测量。文献也把组数作为另一种训练量定义，不可混为一谈。详细边界见 [训练量来源核查](TRAINING_VOLUME_SOURCES_2026-11-10.md)。
- [Hevy 自重统计说明](https://help.hevyapp.com/hc/en-us/articles/38387131819799-Weighted-Bodyweight-Exercises-Is-Bodyweight-Included-in-Volume)显示其是否计入体重取决于动作类型和来源；不代表 Nerae 的自重规则已定。
- 用户希望按动作累计，以支持未来周/月/季/半年/年报告。[Hevy 统计说明](https://help.hevyapp.com/hc/en-us/articles/35702030346903-Hevy-Statistics-Explained-Track-Your-Training-Progress-and-Muscle-Growth)描述了月度汇总和多时间段统计；仅支持这类周期分析有产品先例，不代表这些具体周期、展示或自动推送已在 Nerae 实现。
- 双哑铃需要明确用户记录的重量含义：[Hevy Trainer 说明](https://help.hevyapp.com/hc/en-us/articles/43572343844247-How-Hevy-Trainer-Settings-Work)中，器械设置选的是单只哑铃重量，双哑铃动作的建议重量显示两只合计；此行为仅限该资料所述的 Trainer 场景。[Strong 哑铃计数说明](https://help.strongapp.io/article/101-how-are-dumbbell-exercises-counted)提供是否对哑铃重量翻倍计入总量的规则，但页面最后更新于 2021 年，不能假定现今各端行为未变。用户已确定双哑铃卧推同重同时的情形填写单只重量，由 App 算两只合计；其他场景仍需确认。
- [Fitbod 单侧动作说明](https://help.fitbod.me/hc/en-us/articles/29486697282711-Exercise-Details-Screen-Sets-Reps-and-Weight-Fields-Explained)：单侧动作会说明次数是每只手或每条腿的次数；器械类单侧动作的重量不自动翻倍。[StrengthLog 单侧动作说明](https://help.strengthlog.com/help-article/one-sided-exercises/)的搜索收录文本描述了另一种按一侧录入、自动将统计量翻倍的做法；当次打开原文页面返回 403，故未能核实其所有例外。竞品口径不同，无法直接推出 Nerae 规则。用户已选择左右各做一遍作为同一组、填一次默认相同并可分别修改；只做一侧则在同一组标明另一侧未做，仅统计实际完成的一侧。未填和不等量的保存/统计等边界仍待确认。

设计推论：针对回看这次到底完成了什么，逐组事实比一个汇总数更直接；汇总可作为了解累计负荷、比较同一动作历史的补充。这里的优先顺序是基于任务和用户意见的建议，不声称是竞品已验证的统一页面顺序；统计名称、范围、缺失数据、自重和器械算法仍须单独确认。
