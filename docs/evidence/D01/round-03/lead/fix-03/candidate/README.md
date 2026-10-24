# fix03过程检查，尚未接受

源码仍由原Harness任务修改。SOURCE_MANIFEST为起点，POST_QA_SOURCE_CHECK记录期间base.css/utils/test/README仍有变化，因此本目录是过程诊断，最终接受必须另冻结/复测，不能把候选图片直接改成接受版本。

R15a的200%收藏命中区与R16的200%字段边界已实际改善；R15b未聚焦链接完整隐藏，聚焦140ms transform结束后完整显示并Enter到#main。R15b-keyboard.json最初读到动画首帧，不能据此判断聚焦隐藏；只使用R15b-keyboard-stable.json与对应截图作焦点呈现证据。

普通字号五档回归发现文本tab/桌面导航宽30.6及普通320编辑器右边323.5裁切，见normal-mode-followup；仍属REVIEW_03的R15a/R16。控件高度43.99988等为浮点误差，不作低于44的实质失败；原始数值全部保留，不隐藏记录。
