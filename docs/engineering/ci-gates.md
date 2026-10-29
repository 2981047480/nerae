# 基础检查与 CI

F01-05 正在建设统一入口；当前命令仅覆盖已有工程壳和静态规则，不代表业务、真实 MySQL/Redis 集成、原生安装或发布通过。需要 Git 完整历史、本地安装 `.node-version`、`.go-version`、`package.json` 中固定的 pnpm，并按 `api/test-requirements.txt` 安装 Python 3.11+ 测试依赖；用 `pnpm install --frozen-lockfile --ignore-scripts` 安装项目依赖。

```sh
make check-foundation
make check-planning
make contract-check
make boundary-check
```

可用 `NERAE_PYTHON`、`NERAE_NODE`、`NERAE_PNPM`、`NERAE_GO`、`NERAE_GOFMT` 指定相应可执行文件。Go 默认禁止自动切换工具链。合同门禁固定校验当前工作树的 OpenAPI，不受 `NERAE_OPENAPI` 临时覆盖影响。CI 的检查调用同一个 `make check-foundation`，并取得完整 Git 历史以核验历史验收证据；GitHub Actions 实际托管运行需另留证据。

`check-foundation` 包含版本、任务图、非空合同/单元测试、Go 格式/静态检查/竞态测试、TS/JS 和 Vue 脚本格式、客户端与后台类型检查/构建以及依赖边界正反例。构建分别使用新的临时输出目录并验证关键产物非空，输出目录会保留供检查。客户端构建只生成 H5、小程序和 App 资源，不是原生安装包。`codegen-check`、`check-isolation`、`migration-check`、`test`、`test-integration`、`e2e`、`e2e-api`、`visual-diff`、`seed-check`、`seed-dry-run` 和总 `check` 仍明确返回非零 `NOT_READY`，待对应任务实现，不能据此声称已验收。

Go 检查解析全部生产源文件的 import（包括 build tag），客户端检查解析 Vue/TypeScript/JavaScript 导入并追踪内部依赖；当前只覆盖已定义的目录层次和导入类型。模块 Go 文件须置于 `domain`、`application`、`transport`、`handler`、`repository`、`infrastructure`、`persistence`、`api` 或 `contract` 目录；跨模块 import 仅允许 `domain`、`application`、`api`、`contract`。需要新层次时先调整检查与反例。视图依赖图中的外部库仅允许已列明的 Vue、uni-app 基础库和 vue-i18n，新增外部库须复核并补规则；指向扫描范围之外的源码也会失败。生产 Go 源码不得 import `testdata`。无法追踪的 Worker / Service Worker、`import.meta.glob`、动态 import/require 和代码求值目前显式失败。它不能证明 SQL 表写入归属、运行时网络、授权或动态执行安全，真实服务与安全验收仍由业务及集成卡完成。

TS 格式使用锁文件中的 TypeScript 格式化接口；Vue 模板与样式只检查基础空白，不声称覆盖其全部格式或视觉。

`make codegen-check` 使用锁定的生成器重新生成 TS/Go 类型两次，检查与仓库生成物一致，并运行记录/身份类型与 Go schema 负例。生成器工具需先在 `scripts/codegen/npm` 执行锁定的 `npm ci --ignore-scripts`；缺工具、漂移或测试失败均为非零退出。生成代码不经人工格式化，格式门禁只排除该固定生成文件。静态生成检查不代表真实 HTTP 或数据库已联调。
