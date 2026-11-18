# U02-01 格式 2 计量输入候选

- 状态：候选，未接受。自由训练自定义动作可在本机选择合计、每只器械、左右分开、自重、自重加负重、自重减助力或手填记录重量，并写入既有格式 2 测量对象。
- 左右：先填一侧时，另一侧在被单独修改前使用相同数值；明确标成未做的一侧不进入累计。体重：修改一次会更新本次训练中所有自重组的体重，不改手填记录重量，也不写入身体数据曲线。
- 未做：计划带入、动作库身份、历史重量带入、教练只看计算结果、标准动作同名。服务端合同与累计口径原本已接受这些方法，本轮没有改 OpenAPI。
- 验证：`vue-tsc --noEmit -p apps/client/tsconfig.json` 通过。`apps/client/src/features/training/*.test.mjs` 456 项通过，覆盖先填右侧、未做后再填写不恢复旧数，以及未做一侧草稿残留重量不阻断冲突复核。`scripts/plan.py check` 通过。Go `./internal/modules/workout/domain` 与 `transport` 通过；无测试库时数据库用例按原设计跳过。允许 esbuild 安装脚本后，小程序资源 `uni build -p mp-weixin` 完成。`uni build -p app` 仍报找不到 `@dcloudio/uni-uts-v1`，没有产出 `app-service.js`。没有 iOS 模拟器或真机。这些通过项不是界面验收。
