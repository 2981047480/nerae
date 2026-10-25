# fix06 同版单位一致性静态复核

仍是原 C03 单位修复，不新增页面、不改变视觉或正式契约。Lead 在第13轮仍运行时读到的当前源码观察，尚未冒称新构建浏览器复现。已看到 resolveSetConflict / recordChart 按新kind传播，继续保留；不重做。

收束前请检查两个同一范围的剩余文字出口：

- `utils/format.ts:lastReferenceLabel` 只有 LastReference 参数：reps 固定“次”，seconds 未使用 perSide。`utils/training.ts:referenceText` 直接调用，因此已有猫牛式记录后的上次参考会缺“呼吸”，已有侧桥参考缺“/侧”。保持实际记录的单位与每侧语义；可窄传 kind，不改旧事实、不添加假值。
- `store.ts:ensureTrainingConflict` 的自重分支对 breath 也写成“自重 × 8 次”，remoteText 的 durationPerSide 也只写“42 秒”。预览冲突是 UI 接受范围，要与当前剂量单位一致，而不是借旧文字掩盖实际数据。保留非负重的 null kg、实际 reps/seconds、只确认一组；local/remote 选择仍走实际验证，不杜撰重量。

这是针对当前源码的检查点，不是宣称整轮失败。请与既有 fix06 最新引用/自重/呼吸/错误恢复一起正常交回，同 Flash Medium / 原树 / 4187，所有固定 QA 端口保留。相关正反例可用新完成训练后再次开始同内容的上次参考，以及适用的新单位冲突本地/远端；报告区分模块与实际浏览器证据。
