# fix09：记录摘要必须保留真实组配对与每侧单位

同一 Harness 会话 / OpenCode Go / deepseek-v4.1-flash / Medium，原树 `/private/tmp/nerae-ui-complete`、4187，基线 `70803d1e03dfdbcea524d9f9f5152b22e2f3d102`。只修 S05 折叠记录摘要及有效回归；不重做 UI、样式、路由、store 恢复、图表或其他已经通过的功能。Lead 不写 UI。

最终源708的113测试、type/build、29接缝全过；220尺寸、96隔离状态、F03新版采用、F04双方发送/重试/私稿/撤权均有同源实际UI证据。保留这些结果，不重做它们。

EA37有实际UI复现：默认3组，第四组输入 **20 kg × 5 次**，完成其余组后记录页摘要显示 **20 kg × 10 次 · 4组**；明细是12.5×10、12.5×10、12.5×9、20×5，根本没有20×10这一组。证据 `lead/expansion/frozen-09/flows/ea37-summary-invents-twenty-by-ten.txt` 和同名截图。

同一函数也把新自重每侧鸟狗式摘要显示成“自重 × 10 次”，侧桥为“30 秒”，漏掉 /侧；明细与图表单位已正确，不得改写原始事实。证据 `frozen-09/flows/units-final-bird-single-comparable-point.txt`、`units-final-side-duration-axis.txt`。根因 `RecordsView.vue:recordExerciseValue` 各自取最大重量和最大次数，且摘要手写单位忽略 perSide/bodyweightPerSide/durationPerSide。

要求：摘要只描述真实存在的一组，或明确只展示某一个有标签的度量，绝不能拼接不同组的值。每侧、秒、呼吸、自重、kg与旧0kg原始事实必须准确。若同一动作存在不同记录方式，不混算不同单位；清楚引导看明细即可。不要为缩略摘要改动历史记录或图表比较规则。所有可见摘要都遵守这一边界。

写入范围：`RecordsView.vue`；如有必要使用现有 `utils/format.ts` 或一个小型纯格式函数及相应测试；禁止改全局样式、共享导航、schema、seed、store恢复。有效测试至少覆盖真实20×5与12.5×10不拼接、每侧次数/秒/kg、呼吸、旧0kg、混方式不合并。实际390 UI确认摘要与明细一致，320/200无溢出。Lead会重走这些操作及该页尺寸/状态，并以源文件SHA核对未改页面的既有证据。

完成后 typecheck/build/全部测试，fix09报告md/json、三份source/evidence/report SHA清单，排除dev日志，按原模式自然正常交回。不要自标accepted，不切模型，不新增router，不改其他页。
