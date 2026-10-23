# 第二回合首批验收续查

UI源码仍为未接受检查点1ad6398，Lead只作验收，没有改UI、接受首批或扩展剩余界面。原生Harness锁定，但IAB预览可操作，因此完成独立浏览器检查并核实受支持接续入口。唯一返工真源REVIEW_01.md，现到R13；返工尚未派发。

## 实际浏览器证据

| 路径 | 实测结果 | 证据 |
|---|---|---|
| 响应式 | 今天/发现/训练/教练审阅及编辑，各320/390/430/768/1440 CSS宽度无横向溢出；保存截图不等于视觉接受。教练main重复，训练#main缺失。 | audit2-verified-captures.jsonl、verified系列截图、training-header-geometry |
| 短屏主操作 | 515×629下CTA为y558–606、导航从568开始，CTA中心命中“发现”，R08失败。320×568主操作可达不能覆盖此断点。 | today-short-cta-hit-test、verified-today-515-short-final |
| 触达/阅读 | 名称输入29px、训练日名称34px无关联label，失败；数字框虽然29px，但关联label约48px，未一概判失败。24字中文完整换行，无横向溢出；关键前后值12px仍需修复。 | long-chinese、responsive、R06/R10及可靠截图 |
| 弹层焦点 | 正向Tab循环及取消恢复通过；刚开即Shift+Tab逃出、成功后原触发失效焦点落BODY，失败。polite成功宣告存在。 | dialog-focus、dialog-initial-reverse-focus、published-local |
| 草稿/发布 | 首动作4改5组后保存仍7，失败；本地发布7后学员现用6保持、重复发布禁用；缺名称阻断并聚焦；后续编辑草稿8与交付快照问题见R04。 | save-version、published-local、missing-field及初始模块诊断 |
| 筛选/收藏 | 取消保持原列表；器械应用2份→饮食3份→回训练5份，偏好丢失（R12）；饮食没有错误继承器械。收藏刷新保留。 | filter-switch、favorite-reload、discover-diet |
| 全部训练 | 0次数阻断且不计组；普通模式完成剩余9组至12/12、4动作，单侧/秒正确；回今天和刷新仍完成，记录弹层完整且不能再记组。 | reps-zero、training-normal-complete、complete-today-reload、complete-reopen |
| 减少动态效果 | 预览开关为reduced时训练过渡实测1e-06s；同样完整走12组，最终DOM只忽略实际时间后与普通模式完全相同。但Teleport仍0.22s、html仍smooth，R11失败。 | training-reduced-complete、normal-reduced-equivalence、reduced-motion-teleport |
| 休息 | 从01:30真实递减到01:21；暂停跨刷新保持01:21，加15秒为01:36；恢复计时退出再返回为01:26。反馈仍错指第3组，R02失败。 | rest-initial/paused/paused-reload/extend-resume-exit/return，含Node观测时间与真实DOM |
| 教练内容 | 切下肢日有完整4动作12组；移除高位下拉后及时撤销，内容和总数恢复。此前移除划船的撤销已过期，未假称那次成功。 | coach-remove-undo及实际DOM记录 |
| 动画执行 | 最近30条真实Transition警告指ExploreView/CoachReviewView非元素根无法动画，R13失败；error查询为空。不因CSS标时长接受动画。 | browser-console |
| QA收尾 | 经可见预览→重置→确认，再刷新恢复3/12、root full，清除收藏/草稿/交付/训练修改。标签4留待续，隐藏后台1440×900。 | final-reset、verified-final-reset-today-1440 |

表中简写均指本目录audit2-前缀文件，首批通过边界仍看REVIEW_01。真实浏览器经过时间没有用假时钟替代；源码诊断的Node替身与浏览器证据分开。

## 仪器边界与校正

早期audit2-responsive/today-cta及未标verified的截图含宿主显隐缩放/clip偏差，仅保留校准过程。384/266误标已记录，不算指定宽度验证。后来固定隐藏浏览器，DPR1稳定后按CSS单位截图，前后w/h/dpr一致才标verified；它只表示视口匹配，不表示产品通过。

verified-training-complete-320的scrollY=120，图像固定定位有偏差，弃作视觉依据，改用scrollY=0的verified-training-complete-top-320。部分早期弹层截图与DOM终态不符，未据此新增布局失败；DOM发布按钮bottom552<568，可达。训练header的进度框0尺寸源自窄屏隐藏，未误判预览按钮遮挡。CTA遮挡的导航几何只取可见导航，避免读到隐藏桌面导航。

complete-reopen中的normalFacts空数组来自只适用于训练页的main .record-group选择器，不作内容缺失证据；实际snapshot含完整记录。结果等价使用真正完成两条路径的完整DOM，比较为true。一次不支持的heading level选项匹配两标题，改用已观察main h1后完成；一次records未关时点外部预览只关闭弹层，核对状态后改为明确关闭/等待再操作，未当应用失败。

只读接口不提供document.getAnimations，未测帧率。系统Reduce Motion未切换，上述为预览开关证据。200%文字、原生输入/软键盘、系统偏好、真实浏览器存储失败注入及修复后同版本动效复测未做。已发现实质缺陷后不无限扩大同一版本检查，下一步应交回修复。

## 接续阻塞

原生工具仍报告Mac锁定且无法自动解锁，人工解锁问题已存在，未重复发卡。官方dsh CLI的desktop profile由Electron独占，本地web需要认证；没有复制认证、另建profile或静默换独立OpenCode，见HARNESS_WEB_CLI_ASSESSMENT.md。

锁屏仅阻止原生Harness派发/执行与原生验收，不再扩大为全部IAB不可用。第二个连续goal回合，取得实质新QA和入口证据，goal保持active；若下一回合同一条件仍在且无独立进展，应按三回合规则设blocked并停止，不无限续查日志。Lead未代写UI。
