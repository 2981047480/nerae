# Q02-01 历史验收证据校验

状态：review，未接受。Lead 当前 GPT-6 会话实施；工具未报告具体模型和推理档位。基线 `839e242a6357895c86cd4990bb3163f7b6b5c921`。任务修复 K01-02 修改共享 OpenAPI、ER 与探针后，任务图对 K01-01 的历史证据误报哈希冲突的问题。

检查器以任务的 `accepted_revision` 为被验证版本，以 `source_revision` 为证据文件实际进入 Git 的版本，后者缺省为前者。它验证完整 commit、祖先关系、受控仓库根、普通文件 blob 和 SHA-256；不执行 shell，不接受符号链接、子模块、其他历史分支或 Git 环境变量重定向。代码更新不会使过去接受时的文件内容发生变化；当下集成仍必须独立复验，Git 对象与哈希不证明运行命令或 reviewer 判断真实性。

已核对 D01、D01-02、D01-03、D01-I 的部分历史报告未在原接受提交中落盘，但其登记摘要与后续归档提交 `e4610a7e69fb926782200a1c89373b3ba427fc93` 的 blob 一致；为这些条目显式登记该来源，原 `revision`、`accepted_revision` 和摘要不变。这是归档来源修复，不是对历史视觉、产品或运行时的重新接受。

在临时 Git 仓库先以旧检查器运行新增反例，确认历史文件演进被误拒、伪造提交及 Git 子模块被误接受；修复后验证真实的工作树演进、归档来源、错误摘要、无关提交、链接与环境重定向。最终命令、具体通过结果及独立复核将在集成版本复跑后补录。缺 Git 历史、证据对象或真实运行结果时保持失败，不使用当前文件哈希静默兜底。

2026-10-29 当前工作树运行 `PYTHONDONTWRITEBYTECODE=1 "$NERAE_TEST_PYTHON" -m unittest discover -s scripts/tests -v`：46 项通过；`PYTHONDONTWRITEBYTECODE=1 "$NERAE_TEST_PYTHON" scripts/plan.py check`：退出码 0；`git diff --check`：退出码 0。独立只读复核指出非字符串路径、未提交修改、Git pathspec 与未合并后代提交的测试缺口，已补充对应处理与反例；独立 reviewer 与最终集成版本复跑仍需登记。实际 Python 使用 `/Users/zephyr/Documents/项目/nerae/api/tests/.venv-py311/bin/python`。

独立复核：`/root/contract_tests` 对历史来源、提交关系、输入路径和测试作两轮只读检查；第二轮未发现阻断级问题，并实际复跑 46 项测试和任务图检查通过。`/root/foundation_review` 检查规范及可维护性，提出目录同步和工作树、未合并分支反例，均已补齐。工具未返回子代理实际型号和推理档位，不作猜测。
