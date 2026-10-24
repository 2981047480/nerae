# 过程QA证据边界

此目录为fix02仍运行时的独立实际浏览器检查，accepted=false，不是冻结交回或模型自评。起始43文件清单与QA后复核绑定来源；仅README/STATES改变，渲染文件没有变化。当前仍有REVIEW_03缺陷。

`training-200-320.jpg`捕获期间M03结束导致scroll改变，helper已报excluded，不作为视觉证据；重试同名被wx保护拒绝，未覆盖。只使用`training-200-stable-*`。最初点击旧accessible name“预览”无匹配，新版本实际名称为“打开预览控制”，随后按新DOM操作。没有把选择器错误当产品缺陷。

部分100%今天截图在关闭弹层后的非零scroll位置，尺寸日志虽稳定，也不能从中推断顶部品牌消失。200%今天通过可见品牌按钮回到顶部并记录scroll0。所有截图均默认viewport，记录真实CSS宽高/DPR/scroll；只判记录所覆盖的内容。

根scrollWidth等于视口不足以证明内部可见，R16已通过字段及祖先边界发现被overflow:clip掩盖的超宽。减少动态效果与文字放大是预览控制，不冒称系统设置验证。
