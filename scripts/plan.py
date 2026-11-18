# -*- coding: utf-8 -*-
"""Read-only task validation / packets. Never spawns, edits task state, or merges."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
DEFAULT = ROOT / 'docs/tasks/tasks.json'
STATES = {'planned', 'active', 'review', 'accepted', 'blocked'}
EXECUTABLE = {'task', 'integration'}


def kind(task):
    return task.get('kind', 'work_package')


def completion_errors(task, doc):
    """Check evidence structure; a human still judges its sufficiency."""
    errors = []
    registered = {e['path'] for e in task.get('evidence', [])
                  if isinstance(e, dict) and isinstance(e.get('path'), str)}
    checks = task.get('completion', {})
    rules = doc.get('done_checks', {})
    if not isinstance(checks, dict) or set(checks) != set(rules):
        return ['incomplete completion checklist']
    for name, rule in rules.items():
        result = checks[name]
        if not isinstance(result, dict):
            errors.append(f'{name}: invalid completion result')
        elif result.get('status') == 'not_applicable':
            if not rule['allow_na'] or not result.get('reason', '').strip():
                errors.append(f'{name}: invalid not_applicable')
        elif result.get('status') != 'pass' or not result.get('evidence') or not set(result['evidence']) <= registered:
            errors.append(f'{name}: pass needs registered evidence')
    mode = task.get('review_mode')
    if mode not in {'independent', 'sequential_role_review'}:
        errors.append('missing review mode')
    elif mode == 'independent' and task.get('owner') == task.get('reviewed_by'):
        errors.append('independent review cannot be self review')
    elif mode == 'sequential_role_review' and not task.get('review_reason'):
        errors.append('sequential role review needs reason')
    if kind(task) == 'integration':
        result = task.get('integration_result', {})
        if result.get('integrated_revision') != task.get('accepted_revision'):
            errors.append('integration evidence revision mismatch')
        if not set(task['depends_on']) <= set(result.get('covered_tasks', [])):
            errors.append('integration does not cover dependencies')
        if not result.get('evidence') or not set(result['evidence']) <= registered:
            errors.append('integration needs registered evidence')
    if task.get('onboarding_probe'):
        result = task.get('onboarding_result', {})
        counts = ['extra_context_requests', 'rework_cycles', 'boundary_violations']
        if any(type(result.get(k)) is not int or result[k] < 0 for k in counts):
            errors.append('onboarding missing actual counts')
        if result.get('cold_start') is not True or result.get('verification_reproducible') is not True or result.get('boundary_violations') != 0:
            errors.append('onboarding needs cold start, resolved boundaries and reproducible verification')
        if not result.get('evidence') or not set(result['evidence']) <= registered:
            errors.append('onboarding needs registered evidence')
    return errors


def local_path(value):
    if not isinstance(value, str):
        return False
    path = Path(value)
    return bool(value) and not path.is_absolute() and '..' not in path.parts and not any(c in value for c in '*?[')


class EvidenceError(ValueError):
    """A recorded acceptance artifact cannot be established from Git history."""


class EvidenceRepository:
    """Read immutable evidence blobs without trusting the mutable worktree."""

    def __init__(self, root):
        self.root = Path(root).resolve()
        self.env = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
        self.env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
                        GIT_NO_REPLACE_OBJECTS='1', GIT_TERMINAL_PROMPT='0')
        self.commits = {}
        self.ancestors = {}
        self.blobs = {}
        self._head = None

    def run(self, *args, allowed=(0,)):
        try:
            result = subprocess.run(['git', '-C', str(self.root), '--literal-pathspecs', *args],
                                    env=self.env, capture_output=True, timeout=10, check=False)
        except (OSError, subprocess.TimeoutExpired) as error:
            raise EvidenceError(f'Git unavailable: {error}') from error
        if result.returncode not in allowed:
            raise EvidenceError('Git history or evidence object unavailable')
        return result

    def head(self):
        if self._head is None:
            top = self.run('rev-parse', '--show-toplevel').stdout.decode().strip()
            if Path(top).resolve() != self.root:
                raise EvidenceError('evidence root is not the Git worktree root')
            self._head = self.run('rev-parse', '--verify', 'HEAD^{commit}').stdout.decode().strip()
        return self._head

    def commit(self, revision):
        if not isinstance(revision, str) or not re.fullmatch(r'(?:[0-9a-f]{40}|[0-9a-f]{64})', revision):
            raise EvidenceError('evidence revision must be a complete commit ID')
        if revision not in self.commits:
            resolved = self.run('rev-parse', '--verify', revision + '^{commit}').stdout.decode().strip()
            if resolved != revision:
                raise EvidenceError('evidence revision is not a commit')
            self.commits[revision] = True
        return revision

    def is_ancestor(self, earlier, later):
        key = (earlier, later)
        if key not in self.ancestors:
            self.ancestors[key] = self.run('merge-base', '--is-ancestor', earlier, later,
                                           allowed=(0, 1)).returncode == 0
        return self.ancestors[key]

    def evidence_hash(self, accepted, source, path):
        accepted, source = self.commit(accepted), self.commit(source)
        if not self.is_ancestor(accepted, source) or not self.is_ancestor(source, self.head()):
            raise EvidenceError('evidence source is outside the accepted integration history')
        if (not isinstance(path, str) or not local_path(path) or '\\' in path
                or any(ord(char) < 32 or ord(char) == 127 for char in path)
                or Path(path).as_posix() != path):
            raise EvidenceError('invalid evidence path')
        key = (source, path)
        if key not in self.blobs:
            result = self.run('ls-tree', '-z', '--full-tree', source, '--', path)
            entries = result.stdout.split(b'\0')
            if len(entries) != 2 or not entries[0] or entries[1]:
                raise EvidenceError('evidence missing from source revision')
            try:
                metadata, name = entries[0].split(b'\t', 1)
                mode, object_type, object_id = metadata.split(b' ', 2)
            except ValueError as error:
                raise EvidenceError('invalid Git evidence entry') from error
            if name != os.fsencode(path) or object_type != b'blob' or mode not in (b'100644', b'100755'):
                raise EvidenceError('evidence is not a regular file in source revision')
            blob = self.run('cat-file', 'blob', object_id.decode('ascii')).stdout
            self.blobs[key] = hashlib.sha256(blob).hexdigest()
        return self.blobs[key]


def overlap(first, second):
    a, b = Path(first), Path(second)
    return a == b or a in b.parents or b in a.parents


def ancestors(tasks, key, seen=None):
    seen = set() if seen is None else seen
    for parent in tasks[key]['depends_on']:
        if parent not in tasks:
            continue
        if parent not in seen:
            seen.add(parent)
            ancestors(tasks, parent, seen)
    return seen


def waves(tasks):
    remaining = set(tasks)
    done = set()
    result = []
    while remaining:
        ready = sorted(key for key in remaining if set(tasks[key]['depends_on']) <= done)
        if not ready:
            raise ValueError('dependency cycle: ' + ', '.join(sorted(remaining)))
        result.append(ready)
        done.update(ready)
        remaining.difference_update(ready)
    return result


def validate(doc, root=ROOT):
    errors = []
    evidence_repository = EvidenceRepository(root)
    rows = doc.get('tasks', [])
    tasks = {row['id']: row for row in rows}
    if not rows or len(tasks) != len(rows):
        errors.append('tasks empty or duplicate id')
    if not doc.get('done_checks'):
        errors.append('missing completion rules')
    deferred = doc.get('deferred_packages', {})
    for key, reason in deferred.items():
        if key not in tasks or kind(tasks[key]) != 'work_package' or not reason:
            errors.append(f'{key}: invalid deferred package')
    required = ['title','batch','role','model_policy','deliverables','acceptance','verification_commands','risks','write_allowlist','inputs','flow_ids','forbidden','failure_protocol']
    for key, task in tasks.items():
        node_kind = kind(task)
        if node_kind not in EXECUTABLE | {'work_package'}:
            errors.append(f'{key}: unknown task kind')
        if node_kind in EXECUTABLE:
            parent = tasks.get(task.get('parent'))
            if parent is None or kind(parent) != 'work_package':
                errors.append(f'{key}: missing work package')
            else:
                if key not in parent['depends_on']:
                    errors.append(f'{key}: package must depend on child')
                for path in task['write_allowlist']:
                    if not any(Path(path) == Path(p) or Path(p) in Path(path).parents for p in parent['write_allowlist']):
                        errors.append(f'{key}: child write outside package: {path}')
            if task.get('parent') in deferred and task['status'] != 'planned':
                errors.append(f'{key}: deferred package cannot execute')
        elif task['status'] in {'active', 'review'}:
            errors.append(f'{key}: work package cannot be dispatched')
        for field in required:
            if not task.get(field):
                errors.append(f'{key}: missing {field}')
        if task.get('status') not in STATES:
            errors.append(f'{key}: unknown status')
        if task.get('model_policy') not in doc.get('model_policies', {}):
            errors.append(f'{key}: unknown model policy')
        if len(task.get('depends_on', [])) != len(set(task.get('depends_on', []))):
            errors.append(f'{key}: duplicate dependency')
        for parent in task.get('depends_on', []):
            if parent not in tasks:
                errors.append(f'{key}: unknown dependency {parent}')
        for parent in task.get('acceptance_requires', []):
            if parent not in tasks:
                errors.append(f'{key}: unknown acceptance dependency {parent}')
        for field in ['inputs','write_allowlist']:
            for path in task.get(field, []):
                if not local_path(path) or not (root / path).resolve().is_relative_to(root.resolve()):
                    errors.append(f'{key}: unsafe {field} path {path}')
                elif field == 'inputs' and not (root / path).is_file():
                    errors.append(f'{key}: input missing {path}')
        if not set(task.get('flow_ids',[])) <= {'F01','F02','F03','F04','F05','F06','F07'}:
            errors.append(f'{key}: unknown flow')
        effort = task.get('effort_days', [])
        if len(effort) != 2 or not (0 < effort[0] <= effort[1]):
            errors.append(f'{key}: invalid effort range')
        elif node_kind in EXECUTABLE and effort[1] > 3:
            errors.append(f'{key}: execution card exceeds 3 days')
        if node_kind == 'work_package' and task.get('breakdown') == 'complete':
            children = [t for t in rows if t.get('parent') == key]
            if not children:
                errors.append(f'{key}: complete breakdown without children')
            elif effort != [sum(t['effort_days'][i] for t in children) for i in range(2)]:
                errors.append(f'{key}: package effort differs from children')
        if task.get('status') in {'active','review','accepted'}:
            if any(tasks.get(p, {}).get('status') != 'accepted' for p in task.get('depends_on', [])):
                errors.append(f'{key}: dependency not accepted')
            if not task.get('owner') or not task.get('actual_model') or not task.get('actual_reasoning') or not task.get('base_revision'):
                errors.append(f'{key}: missing actual owner/model/base revision')
        if task.get('status') == 'accepted':
            if node_kind == 'work_package' and task.get('breakdown') != 'complete':
                errors.append(f'{key}: package breakdown incomplete')
            if key in deferred:
                errors.append(f'{key}: deferred package cannot be accepted')
            if any(tasks.get(p,{}).get('status')!='accepted' for p in task.get('acceptance_requires',[])):
                errors.append(f'{key}: acceptance dependency not accepted')
            if not task.get('reviewed_by') or not task.get('accepted_revision') or not task.get('evidence'):
                errors.append(f'{key}: accepted without review/revision/evidence')
            errors += [f'{key}: {e}' for e in completion_errors(task, doc)]
            for evidence in task.get('evidence', []):
                if not isinstance(evidence, dict) or not local_path(evidence.get('path','')):
                    errors.append(f'{key}: invalid evidence reference')
                    continue
                try:
                    digest = evidence_repository.evidence_hash(
                        task.get('accepted_revision'), evidence.get('source_revision', evidence.get('revision')),
                        evidence['path'])
                    if digest != evidence.get('sha256'):
                        errors.append(f'{key}: evidence hash mismatch: {evidence["path"]}')
                except EvidenceError as error:
                    errors.append(f'{key}: evidence {evidence["path"]}: {error}')
                if not any(Path(evidence['path']) == Path(p) or Path(p) in Path(evidence['path']).parents for p in task['write_allowlist']):
                    errors.append(f'{key}: evidence outside write allowlist')
                if evidence.get('revision') != task.get('accepted_revision'):
                    errors.append(f'{key}: evidence revision mismatch')
    try:
        waves(tasks)
        combined = {key:dict(t, depends_on=list(set(t['depends_on']) | set(t.get('acceptance_requires',[])))) for key,t in tasks.items()}
        waves(combined)
    except ValueError as error:
        errors.append(str(error))
    if not any('dependency' in error for error in errors):
        # Work packages only aggregate child ownership; they cannot execute.
        # Check every executable card, including integration cards, against each
        # other using start-order edges, never acceptance-only requirements.
        keys = sorted(key for key, task in tasks.items() if kind(task) in EXECUTABLE)
        parents = {key: ancestors(tasks,key) for key in keys}
        for i,a in enumerate(keys):
            for b in keys[i+1:]:
                if a in parents[b] or b in parents[a]:
                    continue
                conflicts = [(x,y) for x in tasks[a]['write_allowlist'] for y in tasks[b]['write_allowlist'] if overlap(x,y)]
                if conflicts:
                    errors.append(f'{a}/{b}: unordered write overlap {conflicts[0]}')
    return errors


def ready_tasks(tasks, deferred=()):
    return sorted(key for key,t in tasks.items() if kind(t) in EXECUTABLE and t.get('parent') not in deferred and tasks[t['parent']]['status']!='blocked' and t['status']=='planned' and all(tasks[p]['status']=='accepted' for p in t['depends_on']))


def packet(task, doc):
    if kind(task) == 'work_package':
        children = [t['id'] for t in doc['tasks'] if t.get('parent') == task['id']]
        return '\n'.join([f"# {task['id']} · {task['title']}", '',
            '这是路线图工作包，不能直接派发。请细拆或读取下面的执行卡。',
            '细化状态：' + task.get('breakdown', 'pending'),
            '执行卡：' + (', '.join(children) or '尚未细拆'),
            '暂缓原因：' + doc.get('deferred_packages', {}).get(task['id'], '无额外暂缓；仍须依赖与证据通过'), ''])
    lines = [f"# {task['id']} · {task['title']}", '',
             '这是待执行任务包，不代表任务完成。开始前由 Lead 核实依赖、原生工具可用性和必要授权。',
             f"批次：{task['batch']}；状态：{task['status']}；角色：{task['role']}；类型：{kind(task)}；工作包：{task['parent']}",
             f"模型建议：{json.dumps(doc['model_policies'][task['model_policy']], ensure_ascii=False)}",
             '目标：' + task['deliverables'], '依赖：' + (', '.join(task['depends_on']) or '无'),
             '最终接受前置：' + (', '.join(task.get('acceptance_requires',[])) or '无额外任务'),
             '', '## 必读与写入边界']
    lines += ['- 读取：' + p for p in task['inputs']]
    lines += ['- 仅可写：' + p for p in task['write_allowlist']]
    lines += ['- ' + p for p in task['forbidden']]
    lines += ['', '## 接受标准'] + ['- ' + a for a in task['acceptance']]
    lines += ['', '## 验证'] + ['- ' + c for c in task['verification_commands']]
    lines += ['', '命令是该任务完成时必须存在并执行的验收接口；当前业务骨架尚未建立。缺命令/凭据必须报告，不能当作成功。',
              '', '## 风险'] + ['- '+r for r in task['risks']]
    lines += ['- 外部条件：'+r for r in task.get('external_prerequisites',[])]
    lines += ['', '## 完成清单（接受前全部回答）']
    lines += ['- '+key+': '+rule['description']+('；不适用须理由与review' if rule['allow_na'] else '；必须通过') for key,rule in doc['done_checks'].items()]
    if kind(task) == 'integration':
        lines += ['', '集成卡：只接线/验证；填写integration_result，列全部直接依赖、实际集成提交与同版本证据。不新增业务。']
    if task.get('onboarding_probe'):
        lines += ['', '陌生执行者上手卡：不预载聊天历史；填写onboarding_result的真实额外上下文请求/返工/边界与复跑记录，不预填成功。']
    lines += ['', '## 返回报告',
              f"报告目录：docs/evidence/{task['parent']}/{task['id']}/（必须在写入白名单内）。",
              'status: success / partial / blocked / failed；列实际模型/推理、基线和提交、改动文件、命令退出码、截图/日志证据、未通过项、用户影响、建议下一步与需 Lead 决策。',
              '遵循 APP_FLOW 的用户/App/AI责任分工；用户决定与确认不可由模拟点击或默认勾选代替。',
              task['failure_protocol']]
    return '\n'.join(lines)+'\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check','ready','waves','packet','catalog'])
    parser.add_argument('task_id', nargs='?')
    parser.add_argument('--file', type=Path, default=DEFAULT)
    args = parser.parse_args()
    try:
        doc = json.loads(args.file.read_text(encoding='utf-8'))
        errors = validate(doc)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f'Invalid plan: {error}',file=sys.stderr)
        return 1
    if errors:
        print('\n'.join(errors),file=sys.stderr)
        return 1
    tasks = {t['id']:t for t in doc['tasks']}
    if args.command == 'check':
        print(json.dumps({'valid':True,'work_packages':sum(kind(t)=='work_package' for t in tasks.values()),'execution_cards':sum(kind(t) in EXECUTABLE for t in tasks.values()),'accepted':sum(t['status']=='accepted' for t in tasks.values()),'ready':ready_tasks(tasks, doc.get('deferred_packages',{}))},ensure_ascii=False))
    elif args.command == 'ready':
        for key in ready_tasks(tasks, doc.get('deferred_packages',{})): print(key+' '+tasks[key]['title'])
    elif args.command == 'waves':
        print('预测依赖层；不等于已放行，并发上限与外部条件仍需 Lead 核对。')
        for i,group in enumerate(waves(tasks),1): print(str(i)+': '+', '.join(group))
    elif args.command == 'catalog':
        print('# 开发任务目录\n\n由 `scripts/plan.py catalog` 从 tasks.json 生成；JSON 是唯一状态源。工作包不能直接派发，执行/集成卡不超过 3 有效人日。估工不含外部等待。三端同步推进，近期实施与阶段验收见[首版交付计划](../RECORDING_DELIVERY_PLAN.md)；所有自有 UI 遵守[白底灰阶规范](../design/MONOCHROME_COLOR_SYSTEM.md)。各卡按自身依赖开始和验收。\n')
        print('| ID | 用户结果 / 任务 | 类型/工作包 | 开始依赖 | 接受前置 | 人日假设 | 状态 |')
        print('|---|---|---|---|---|---|---|')
        for t in doc['tasks']:
            label = kind(t)+' / '+t.get('parent',t.get('breakdown','pending'))
            if t['id'] in doc.get('deferred_packages',{}): label += ' / 暂缓'
            print('| '+' | '.join([t['id'],t['title'],label,', '.join(t['depends_on']) or '—',', '.join(t.get('acceptance_requires',[])) or '—',str(t['effort_days'][0])+'–'+str(t['effort_days'][1]),t['status']])+' |')
    elif args.task_id not in tasks:
        print('Unknown task id',file=sys.stderr)
        return 1
    else:
        print(packet(tasks[args.task_id],doc),end='')
    return 0


if __name__ == '__main__':
    sys.exit(main())
