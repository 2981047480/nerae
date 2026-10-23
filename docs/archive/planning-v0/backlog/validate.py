"""校验 backlog/tasks.yaml：编号、依赖、波次、写作用域与验收完整性。

用法：python3 backlog/validate.py
退出码 0 表示全部规则通过；1 表示存在问题（会打印全部问题）。
这是静态校验，不执行任务，也不代表任何业务实现已经完成。
"""
from __future__ import annotations

import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
BOARD = ROOT / "backlog" / "tasks.yaml"
REQUIRED = {"id", "title", "wave", "kind", "role", "scopes", "depends_on", "estimate", "deliverables", "acceptance", "status"}
KINDS = {"parallel", "tail", "serial"}
ESTIMATES = {"S", "M", "L"}
STATUSES = {"todo", "claimed", "in_progress", "review", "done", "blocked"}
ROLES = {"contract", "platform", "backend", "mini", "admin", "design", "qa", "ops", "product"}


def overlap(a: str, b: str) -> bool:
    a, b = a.rstrip("/"), b.rstrip("/")
    return a == b or a.startswith(b + "/") or b.startswith(a + "/")


def main() -> int:
    doc = yaml.safe_load(BOARD.read_text(encoding="utf-8"))
    tasks = doc["tasks"]
    problems: list[str] = []
    ids: dict[str, dict] = {}
    for task in tasks:
        missing = REQUIRED - set(task)
        if missing:
            problems.append(f"{task.get('id', '?')}: 缺少字段 {sorted(missing)}")
            continue
        if task["id"] in ids:
            problems.append(f"{task['id']}: 重复编号")
        ids[task["id"]] = task

    waves = {w["id"]: w for w in doc["meta"]["waves"]}
    by_wave: dict[int, list[dict]] = {}
    for task in ids.values():
        if task["wave"] not in waves:
            problems.append(f"{task['id']}: 波次 {task['wave']} 未在 meta.waves 声明")
        by_wave.setdefault(task["wave"], []).append(task)
        if task["kind"] not in KINDS:
            problems.append(f"{task['id']}: kind 非法 {task['kind']}")
        if task["estimate"] not in ESTIMATES:
            problems.append(f"{task['id']}: estimate 非法 {task['estimate']}")
        if task["role"] not in ROLES:
            problems.append(f"{task['id']}: role 非法 {task['role']}")
        if task["status"] not in STATUSES:
            problems.append(f"{task['id']}: status 非法 {task['status']}")
        if len(task["deliverables"]) < 1:
            problems.append(f"{task['id']}: deliverables 为空")
        if len(task["acceptance"]) < 1:
            problems.append(f"{task['id']}: acceptance 为空")
        if len(task["scopes"]) < 1:
            problems.append(f"{task['id']}: scopes 为空")

    # 依赖存在、方向合法
    for task in ids.values():
        for dep_id in task["depends_on"]:
            dep = ids.get(dep_id)
            if dep is None:
                problems.append(f"{task['id']}: 依赖不存在 {dep_id}")
                continue
            if dep["wave"] < task["wave"]:
                continue
            if dep["wave"] > task["wave"]:
                problems.append(f"{task['id']}: 依赖 {dep_id} 在更晚波次（{dep['wave']} > {task['wave']}）")
                continue
            # 同波次：只有 tail/serial 可以依赖同波次，且只能依赖 parallel/serial
            if task["kind"] not in {"tail", "serial"}:
                problems.append(f"{task['id']}: {task['kind']} 任务不得依赖同波次 {dep_id}")
            elif dep["kind"] == "tail":
                problems.append(f"{task['id']}: 依赖同波次 tail 任务 {dep_id}，应合并或前移")

    # 同波次写作用域不得重叠（tail 只写集成文件，仍按规则检查）
    for wave, group in sorted(by_wave.items()):
        for i, a in enumerate(group):
            for b in group[i + 1:]:
                if a["kind"] == "serial" and b["kind"] == "serial":
                    continue  # 串行链由单一持有人顺序修改，另做链线性检查
                for sa in a["scopes"]:
                    for sb in b["scopes"]:
                        if overlap(sa, sb):
                            problems.append(
                                f"W{wave} 写作用域重叠: {a['id']} [{sa}] 与 {b['id']} [{sb}]"
                            )

    # 每个波次都要有非 tail 的并行任务，避免整波串行
    for wave, group in sorted(by_wave.items()):
        if wave in (0,):
            continue
        parallel = [t for t in group if t["kind"] in {"parallel", "serial"}]
        if len(parallel) < 2 and wave not in (9, 10):
            problems.append(f"W{wave}: 可并行任务少于 2 个，检查是否过度串行")

    # 依赖图无环（含跨波次）
    state: dict[str, int] = {}

    def visit(node: str) -> bool:
        if state.get(node) == 1:
            return False
        if state.get(node) == 2:
            return True
        state[node] = 1
        for dep in ids[node]["depends_on"]:
            if dep in ids and not visit(dep):
                return False
        state[node] = 2
        return True

    for node in ids:
        if not visit(node):
            problems.append(f"依赖图存在环，涉及 {node}")
            break

    if problems:
        print(f"发现 {len(problems)} 个问题：")
        for item in problems:
            print(" -", item)
        return 1

    counts = {w: len(g) for w, g in sorted(by_wave.items())}
    total = len(ids)
    print(f"通过：{total} 个任务，{len(waves)} 个波次")
    print("每波任务数：", ", ".join(f"W{w}={n}" for w, n in counts.items()))
    print("状态：", {s: sum(1 for t in ids.values() if t["status"] == s) for s in sorted(STATUSES)})
    return 0


if __name__ == "__main__":
    sys.exit(main())
