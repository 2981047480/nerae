# D01-03 fix07 候选报告（待 Lead 冻结复验）

状态：**candidate / 尚未 accepted**。基线为 Lead 冻结 `54b74c86b93e80967b84d9fff4cba95fd0be9ba1`；未改 Git HEAD，4186/4188/4189/4190/4191 未触碰。预览 `http://127.0.0.1:4187/`，PID 64273。

## 记录序列

`recordChart` 以最新记录的 kind 选定 series，只纳入同一 kind 的记录；不同 kind 的旧事实仍保留在明细，不互相连线/相减。390 实际完成 Core 8 组后选择鸟狗式：只有 10/24 新记录一个点（单位`次/侧`、delta null、无折线、无 `+10`），旧 10/21 `perSide/0kg` 仍在明细；`chart` 轴/aria/来源为“最高次数（每侧）”，哑铃卧推仍按 kg 双点显示。

单点（有且仅有一个可比较点）时只显示“只有一次记录，暂不判断趋势”，不制造增量；`recordSummary` 的 count 仍反映范围内记录数，但 plot 只含同测量语义点。`lastReferenceFor` 只选相同 kind 的旧记录，不把旧 perSide/0kg 套到新 bodyweightPerSide；不兼容时显示“暂无可比较参考”。`lastReferenceLabel` 按 kind 保留“次呼吸/秒/侧”，`ensureTrainingConflict` 同步；旧事实不重写、不删除、不猜迁移。

## S16 文案

服务边界改为“是否展示由‘学员可见’控制；保存不会自动成交”，保留已有约定不变、平台不收款、服务与健康授权分开；390 实际 DOM 新句 true、旧句 false。

## 验证

同版 typecheck **0**、build **0**、vitest **111/111**、exit **0**；日志 `logs/d01-03-fix07-*`。模块覆盖同 kind series、单点无 delta、旧 reference 不兼容、S16 文案；实际 390 浏览器完成 Core 8 组并检查记录图表与明细。两条证据截图/JSON 已落盘。保留 fix06 最新引用采用/旧引用只读、fix05 M02/M12/V04/文案及全部既有回归。

记录明细仍用原有 set/kind 展示旧事实，图表只做可比较投影；轴单位`次/侧`与来源“最高次数（每侧）”一致，负重的 `kg` 与秒的 `秒/侧` 分支不受影响。未测：Lead 全 16 族/55 项/五宽/动效、真实服务端权限/消息/健康鉴权、微信/uni-app 真机及所有单位/系列组合。旧数据不迁移的边界仅在样机内验证。候选未 accepted。

## 散列

源码清单 `logs/d01-03-fix07-source-sha256.txt`，sha256 `a0e08293…`；证据清单 `logs/d01-03-fix07-evidence-sha256.txt`，sha256 `c3cbaaec…`；dev 日志排除。请 Lead 冻结后最终复验/集成。
