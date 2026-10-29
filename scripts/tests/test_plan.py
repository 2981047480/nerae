# -*- coding: utf-8 -*-
import copy
import importlib.util
import json
import hashlib
import os
import subprocess
import tempfile
from unittest.mock import patch
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('plan', ROOT/'scripts/plan.py')
plan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plan)


class PlanTests(unittest.TestCase):
    def setUp(self):
        self.doc = json.loads((ROOT/'docs/tasks/tasks.json').read_text())
        # Scheduling tests use a planned copy; real accepted state remains editable.
        for task in self.doc['tasks']:
            task['status']='planned'
            task['evidence']=[]

    def test_current_plan_valid_and_not_marked_implemented(self):
        self.assertEqual(plan.validate(self.doc), [])
        self.assertTrue(all(t['status']=='planned' for t in self.doc['tasks']))

    def test_unknown_dependency_rejected(self):
        self.doc['tasks'][0]['depends_on'] = ['UNKNOWN']
        self.assertTrue(any('unknown dependency' in e for e in plan.validate(self.doc)))

    def test_cycle_rejected(self):
        self.doc['tasks'][0]['depends_on'] = ['F02']
        self.assertTrue(any('cycle' in e for e in plan.validate(self.doc)))

    def test_duplicate_id_rejected(self):
        self.doc['tasks'].append(copy.deepcopy(self.doc['tasks'][0]))
        self.assertTrue(plan.validate(self.doc))

    def test_work_package_aggregate_paths_do_not_claim_execution(self):
        package = next(t for t in self.doc['tasks'] if t['id']=='D01')
        package['write_allowlist'].append('apps/api/cmd/subpath/')
        self.assertEqual(plan.validate(self.doc), [])

    def test_parallel_executable_owner_collision_rejected(self):
        tasks = {t['id']: t for t in self.doc['tasks']}
        tasks['D01']['write_allowlist'].append('apps/api/cmd/subpath/')
        tasks['D01-01']['write_allowlist'].append('apps/api/cmd/subpath/')
        self.assertTrue(any('D01-01/F01-02: unordered write overlap' in e for e in plan.validate(self.doc)))

    def test_integration_card_still_claims_its_write_scope(self):
        tasks = {t['id']: t for t in self.doc['tasks']}
        tasks['D01']['write_allowlist'].append('tests/foundation/')
        tasks['D01-01']['write_allowlist'].append('tests/foundation/')
        self.assertTrue(any('D01-01/F01-I: unordered write overlap' in e for e in plan.validate(self.doc)))

    def test_unsafe_or_missing_context_rejected(self):
        for bad in ['/tmp/outside','../outside','docs/not-present.md']:
            with self.subTest(path=bad):
                doc=copy.deepcopy(self.doc); doc['tasks'][0]['inputs'].append(bad)
                self.assertTrue(plan.validate(doc))

    def test_accepted_without_real_evidence_rejected(self):
        self.doc['tasks'][0]['status']='accepted'
        self.assertTrue(any('accepted without' in e for e in plan.validate(self.doc)))

    def test_external_acceptance_requirement_cannot_be_skipped(self):
        task=next(t for t in self.doc['tasks'] if t['id']=='A05')
        task['status']='accepted'
        self.assertTrue(any('acceptance dependency not accepted' in e for e in plan.validate(self.doc)))

    def test_active_dependency_not_accepted_rejected(self):
        self.doc['tasks'][1]['status']='active'
        self.assertTrue(any('dependency not accepted' in e for e in plan.validate(self.doc)))

    def test_evidence_digest_must_match(self):
        task=self.doc['tasks'][0]
        rev=subprocess.check_output(['git','-C',str(ROOT),'rev-parse','HEAD'], text=True).strip()
        task.update(status='accepted',owner='test',actual_model='test',base_revision='test',reviewed_by='test',accepted_revision=rev,evidence=[{'path':'README.md','sha256':'0'*64,'revision':rev}])
        self.assertTrue(any('hash mismatch' in e for e in plan.validate(self.doc)))

    def test_packet_contains_bounded_inputs_and_failure_protocol(self):
        task=next(t for t in self.doc['tasks'] if t['id']=='F01-02')
        text=plan.packet(task,self.doc)
        for expected in ['仅可写','验收','failure','F01-02','完成清单','DEFINITION_OF_DONE']:
            if expected=='failure': self.assertIn('blocked',text)
            else: self.assertIn(expected,text)

    def test_ready_only_root_tasks_and_waves_cover_every_task_once(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        self.assertEqual(plan.ready_tasks(tasks, self.doc['deferred_packages']), ['D01-01','F01-01','K01-01','L01-01'])
        ordered=[k for group in plan.waves(tasks) for k in group]
        self.assertEqual(len(ordered),len(set(ordered)))
        for key,t in tasks.items():
            for dep in t['depends_on']: self.assertLess(ordered.index(dep),ordered.index(key))

    def task(self, key):
        return next(t for t in self.doc['tasks'] if t['id']==key)

    def accepted_fixture(self, node_kind='task'):
        # Real-file digest validates the checker, not any production completion.
        doc=copy.deepcopy(self.doc)
        child=copy.deepcopy(self.task('F01-01'))
        parent=copy.deepcopy(self.task('F01'))
        revision=subprocess.check_output(['git','-C',str(ROOT),'rev-parse','HEAD'], text=True).strip()
        blob=subprocess.check_output(['git','-C',str(ROOT),'cat-file','blob',revision+':README.md'])
        child.update(kind=node_kind, depends_on=[], write_allowlist=['README.md'], inputs=['README.md'], status='accepted', owner='worker-fixture', actual_model='model-fixture', actual_reasoning='medium', base_revision='base-fixture', accepted_revision=revision, reviewed_by='reviewer-fixture', review_mode='independent')
        child['evidence']=[{'path':'README.md','sha256':hashlib.sha256(blob).hexdigest(),'revision':revision}]
        child['completion']={k:{'status':'pass','evidence':['README.md']} for k in doc['done_checks']}
        parent.update(depends_on=[child['id']],write_allowlist=['README.md'],inputs=['README.md'],effort_days=child['effort_days'])
        doc.update(tasks=[parent,child],deferred_packages={})
        return doc,child

    def test_valid_evidence_and_checklist_can_be_accepted(self):
        doc,_=self.accepted_fixture()
        self.assertEqual(plan.validate(doc),[])

    def test_work_package_packet_is_not_a_dispatch_packet(self):
        text=plan.packet(self.task('F01'),self.doc)
        self.assertIn('不能直接派发',text)
        self.assertIn('F01-01',text)
        self.assertNotIn('仅可写',text)

    def test_work_package_cannot_be_active(self):
        self.task('F01')['status']='active'
        self.assertTrue(any('cannot be dispatched' in e for e in plan.validate(self.doc)))

    def test_execution_card_over_three_days_rejected(self):
        self.task('F01-02')['effort_days']=[1,4]
        self.assertTrue(any('exceeds 3 days' in e for e in plan.validate(self.doc)))

    def test_child_cannot_escape_package_write_domain(self):
        self.task('F01-02')['write_allowlist'].append('unowned/location/')
        self.assertTrue(any('outside package' in e for e in plan.validate(self.doc)))

    def test_package_must_depend_on_every_child(self):
        self.task('F01')['depends_on'].remove('F01-02')
        self.assertTrue(any('must depend on child' in e for e in plan.validate(self.doc)))

    def test_complete_package_requires_children_and_summed_estimate(self):
        # A01 now has an implementation child; construct the missing-child
        # condition explicitly instead of assuming a particular package stays empty.
        self.doc['tasks']=[task for task in self.doc['tasks'] if task.get('parent') != 'A01']
        self.task('A01')['breakdown']='complete'
        self.task('F01')['effort_days']=[1,2]
        errors=plan.validate(self.doc)
        self.assertTrue(any('without children' in e for e in errors))
        self.assertTrue(any('effort differs' in e for e in errors))

    def test_deferred_work_is_never_ready(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        self.assertEqual(plan.ready_tasks(tasks, {'F01':'test hold'}),['D01-01','K01-01','L01-01'])

    def test_blocked_work_package_stops_its_ready_children(self):
        self.task('F01')['status']='blocked'
        tasks={t['id']:t for t in self.doc['tasks']}
        self.assertEqual(plan.ready_tasks(tasks,self.doc['deferred_packages']),['D01-01','K01-01','L01-01'])

    def test_contract_and_platform_gates_hold_downstream_work(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        # Generation can start from the accepted recording contract baseline,
        # but final acceptance still requires the complete foundation and contract packages.
        for name in ('F01-01', 'F01-02', 'F01-03', 'Q02-02'):
            tasks[name]['status']='accepted'
        self.assertNotIn('F02-01', plan.ready_tasks(tasks, self.doc['deferred_packages']))
        tasks['K01-01']['status']='accepted'
        self.assertIn('F02-01', plan.ready_tasks(tasks, self.doc['deferred_packages']))
        self.assertTrue({'F01', 'K01'} <= set(tasks['F02-01']['acceptance_requires']))
        # Platform and current design must precede any future U01 execution.
        self.assertTrue({'P02-01','D02'} <= plan.ancestors(tasks,'U01'))
        self.assertIn('P02-01', plan.ancestors(tasks,'V01'))

    def test_installed_app_builds_gate_acceptance_and_m1(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        builds={'P02-02','P02-03'}
        self.assertTrue(builds <= set(tasks['U01']['acceptance_requires']))
        self.assertTrue(builds | {'U01','U02','I-A02'} <= set(tasks['I-A04']['depends_on']))
        for build in builds:
            self.assertIn('P02-01',plan.ancestors(tasks,build))
            self.assertNotIn('V03',plan.ancestors(tasks,build))

    def test_external_requirements_block_only_the_relevant_acceptance(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        combined={k:dict(t,depends_on=list(set(t['depends_on']) | set(t.get('acceptance_requires',[])))) for k,t in tasks.items()}
        self.assertIn('L01-04',plan.ancestors(combined,'A05'))
        self.assertIn('L01-03',plan.ancestors(combined,'O03'))
        self.assertNotIn('L01-02',plan.ancestors(combined,'A05'))
        self.assertNotIn('L01-02',plan.ancestors(combined,'O03'))
        self.assertTrue({'L01-02','U08','O03'} <= plan.ancestors(combined,'V02'))

    def test_onboarding_is_independent_of_foundation_delivery(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        self.assertFalse(tasks['F02-01'].get('onboarding_probe'))
        self.assertTrue(tasks['Q01-01'].get('onboarding_probe'))
        self.assertIn('Q01',self.doc['deferred_packages'])
        self.assertNotIn('Q01-01',plan.ancestors(tasks,'F02'))
        tasks['F02-01']['status']='accepted'
        self.assertNotIn('Q01-01',plan.ready_tasks(tasks,self.doc['deferred_packages']))

    def test_ui_tasks_receive_current_design_and_deletion_has_owner(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        design={'docs/UI_SPEC.md','docs/design/round-05/BRIEF.md','docs/design/round-05/app-storyboards/README.md'}
        for number in range(1,9):
            task=tasks[f'U{number:02d}']
            self.assertTrue(design <= set(task['inputs']),task['id'])
        self.assertIn('apps/client/src/features/account/',tasks['U08']['write_allowlist'])
        self.assertIn('O03',tasks['U08']['acceptance_requires'])
        self.assertIn('L01-01',tasks['K01-02']['depends_on'])

    def test_m1_estimate_matches_distinct_planned_work(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        foundation=['F01','K01','D02','F02','P02-01','P02-02','P02-03','L01-01']
        product=['A01','A02','I-A01','A03','A04','S01-A','I-A02','U01','U02','I-A04']
        ids=foundation+product
        self.assertEqual(len(ids),len(set(ids)))
        def estimate(group):
            return [sum(tasks[k]['effort_days'][i] for k in group) for i in (0,1)]
        def label(values):
            return f'{values[0]:g}–{values[1]:g}'
        text=(ROOT/'docs/IMPLEMENTATION_PLAN.md').read_text()
        expected={
            '工程、合同、设计和可安装开发包':label(estimate(foundation)),
            '记录领域与三端界面、集成及试用':label(estimate(product)),
            '**合计**':'**'+label(estimate(ids))+'**',
        }
        def verify_rows(document):
            found={name:[] for name in expected}
            for line in document.splitlines():
                if not line.startswith('|'):
                    continue
                cells=[cell.strip() for cell in line.strip().strip('|').split('|')]
                if len(cells)==3 and cells[0] in found:
                    found[cells[0]].append(cells[2])
            for name,value in expected.items():
                self.assertEqual(found[name],[value],name)
        verify_rows(text)
        first=next(line for line in text.splitlines() if line.startswith('| 工程、合同、设计和可安装开发包 |'))
        changed=first.replace('| '+expected['工程、合同、设计和可安装开发包']+' |',
                              '| '+expected['记录领域与三端界面、集成及试用']+' |')
        self.assertNotEqual(first,changed)
        with self.assertRaises(AssertionError):
            verify_rows(text.replace(first,changed,1))
        reserve=next(line for line in text.splitlines() if '预算容量约' in line)
        self.assertIn('**'+label([round(n*1.2,1) for n in estimate(ids)])+' 人日**',reserve)

    def test_partial_package_cannot_be_accepted(self):
        self.task('I01')['status']='accepted'
        self.assertTrue(any('breakdown incomplete' in e for e in plan.validate(self.doc)))

    def test_checklist_missing_or_unregistered_evidence_rejected(self):
        doc,t=self.accepted_fixture()
        t['completion'].pop('scope')
        self.assertTrue(any('incomplete completion' in e for e in plan.validate(doc)))
        doc,t=self.accepted_fixture()
        t['completion']['scope']['evidence']=['not-registered.md']
        self.assertTrue(any('registered evidence' in e for e in plan.validate(doc)))

    def test_not_applicable_needs_reason_and_cannot_skip_core_check(self):
        doc,t=self.accepted_fixture()
        t['completion']['privacy']={'status':'not_applicable','reason':'checker-only fixture; no personal data'}
        self.assertEqual(plan.validate(doc),[])
        t['completion']['privacy']['reason']=''
        self.assertTrue(any('invalid not_applicable' in e for e in plan.validate(doc)))
        t['completion']['scope']={'status':'not_applicable','reason':'cannot bypass required scope check'}
        self.assertTrue(any('scope: invalid' in e for e in plan.validate(doc)))

    def test_old_revision_evidence_rejected(self):
        doc,t=self.accepted_fixture()
        t['evidence'][0]['revision']='older-branch-fixture'
        self.assertTrue(any('evidence revision mismatch' in e for e in plan.validate(doc)))

    def test_valid_digest_cannot_reference_out_of_scope_evidence(self):
        doc,t=self.accepted_fixture()
        t['write_allowlist']=['docs/evidence/F01/F01-01/']
        doc['tasks'][0]['write_allowlist']=t['write_allowlist']
        self.assertTrue(any('evidence outside write allowlist' in e for e in plan.validate(doc)))

    def test_independent_review_cannot_be_self_review(self):
        doc,t=self.accepted_fixture()
        t['reviewed_by']=t['owner']
        self.assertTrue(any('cannot be self review' in e for e in plan.validate(doc)))
        t.update(review_mode='sequential_role_review', review_reason='explicit sequential-role checker fixture')
        self.assertEqual(plan.validate(doc),[])

    def test_integration_requires_same_revision_and_dependency_coverage(self):
        doc,t=self.accepted_fixture('integration')
        self.assertTrue(any('integration' in e for e in plan.validate(doc)))
        t['integration_result']={'integrated_revision':t['accepted_revision'],'covered_tasks':[],'evidence':['README.md']}
        self.assertEqual(plan.validate(doc),[])
        t['depends_on']=['dependency-fixture']
        self.assertTrue(any('does not cover dependencies' in e for e in plan.completion_errors(t,doc)))
        t['integration_result']['integrated_revision']='older-branch-fixture'
        self.assertTrue(any('revision mismatch' in e for e in plan.completion_errors(t,doc)))

    def test_onboarding_requires_actual_counts_and_reproducible_result(self):
        doc,t=self.accepted_fixture()
        t['onboarding_probe']=True
        self.assertTrue(any('onboarding' in e for e in plan.validate(doc)))
        t['onboarding_result']={'cold_start':True,'extra_context_requests':3,'rework_cycles':2,'boundary_violations':0,'verification_reproducible':True,'evidence':['README.md']}
        self.assertEqual(plan.validate(doc),[])
        t['onboarding_result']['verification_reproducible']=False
        self.assertTrue(any('reproducible' in e for e in plan.validate(doc)))


    def test_foundation_checks_can_start_but_not_accept_before_isolation(self):
        tasks={t['id']:t for t in self.doc['tasks']}
        tasks['F01-02']['status']='accepted'
        tasks['F01-03']['status']='accepted'
        tasks['F01-04']['status']='active'
        check=tasks['F01-05']
        check['depends_on']=['F01-02','F01-03']
        check['acceptance_requires']=['F01-04']
        self.assertIn('F01-05',plan.ready_tasks(tasks,self.doc['deferred_packages']))
        check.update(status='active',owner='fixture',actual_model='fixture',actual_reasoning='medium',base_revision='fixture')
        self.assertFalse(any(e.startswith('F01-05: dependency not accepted') for e in plan.validate(self.doc)))
        check['status']='accepted'
        self.assertTrue(any(e.startswith('F01-05: acceptance dependency not accepted') for e in plan.validate(self.doc)))
        check['status']='planned'
        tasks['F01-04']['acceptance_requires']=['F01-05']
        self.assertTrue(any('dependency cycle' in e for e in plan.validate(self.doc)))
        tasks['F01-04']['acceptance_requires']=[]
        for task in self.doc['tasks']:
            task['status']='planned'
        check['write_allowlist'].append('scripts/dev/collision.py')
        self.assertTrue(any('F01-04/F01-05: unordered write overlap' in e for e in plan.validate(self.doc)))


    def test_codegen_can_start_early_without_weakening_final_acceptance(self):
        tasks = {t['id']: t for t in self.doc['tasks']}
        for name in ['F01-01', 'F01-02', 'F01-03', 'K01-01', 'Q02-02']:
            tasks[name]['status'] = 'accepted'
        codegen = tasks['F02-01']
        self.assertIn('F02-01', plan.ready_tasks(tasks, self.doc['deferred_packages']))
        codegen.update(status='accepted', owner='fixture', actual_model='fixture',
                       actual_reasoning='high', base_revision='fixture')
        self.assertTrue(any(e.startswith('F02-01: acceptance dependency not accepted')
                            for e in plan.validate(self.doc)))
        tasks['F01']['status'] = 'accepted'
        tasks['K01']['status'] = 'accepted'
        errors = plan.validate(self.doc)
        self.assertTrue(any(e.startswith('F01: dependency not accepted') for e in errors))
        self.assertTrue(any(e.startswith('K01: dependency not accepted') for e in errors))

    def test_codegen_acceptance_only_dependency_never_orders_writers(self):
        tasks = {t['id']: t for t in self.doc['tasks']}
        tasks['F02-01']['write_allowlist'].append('api/tests/shared-fixture.py')
        errors = plan.validate(self.doc)
        self.assertTrue(any('F02-01/K01-02: unordered write overlap' in e for e in errors))

class GitEvidenceTests(unittest.TestCase):
    """History verification uses disposable repositories, never project data."""

    def setUp(self):
        self.fixtures = PlanTests(methodName="test_current_plan_valid_and_not_marked_implemented")
        self.fixtures.setUp()

    def repository(self):
        temp = tempfile.TemporaryDirectory(prefix='nerae-plan-evidence-test-')
        self.addCleanup(temp.cleanup)
        root = Path(temp.name).resolve()
        def git(*args):
            return subprocess.check_output(['git', '-C', str(root), '-c', 'user.name=Nerae Test',
                                            '-c', 'user.email=nerae-test@example.invalid', *args],
                                           stderr=subprocess.PIPE).decode().strip()
        git('init', '-q')
        (root / 'README.md').write_text('accepted evidence\n')
        git('add', 'README.md')
        git('commit', '-q', '-m', 'accept evidence')
        accepted = git('rev-parse', 'HEAD')
        doc, task = self.fixtures.accepted_fixture()
        task['accepted_revision'] = accepted
        task['evidence'][0].update(revision=accepted, sha256=hashlib.sha256(b'accepted evidence\n').hexdigest())
        return root, git, doc, task, accepted

    def test_historical_file_remains_valid_after_later_change(self):
        root, git, doc, task, accepted = self.repository()
        (root / 'README.md').write_text('later implementation\n')
        git('add', 'README.md'); git('commit', '-q', '-m', 'later change')
        self.assertEqual(plan.validate(doc, root), [])
        self.assertEqual(task['accepted_revision'], accepted)

    def test_historical_file_remains_valid_with_uncommitted_changes(self):
        root, git, doc, _, _ = self.repository()
        (root / 'README.md').write_text('uncommitted implementation\n')
        self.assertEqual(plan.validate(doc, root), [])
        git('add', 'README.md')
        self.assertEqual(plan.validate(doc, root), [])

    def test_later_archive_source_does_not_change_accepted_revision(self):
        root, git, doc, task, accepted = self.repository()
        (root / 'README.md').write_text('archived report\n')
        git('add', 'README.md'); git('commit', '-q', '-m', 'archive report')
        task['evidence'][0]['source_revision'] = git('rev-parse', 'HEAD')
        task['evidence'][0]['sha256'] = hashlib.sha256(b'archived report\n').hexdigest()
        self.assertEqual(plan.validate(doc, root), [])
        self.assertEqual(task['accepted_revision'], accepted)

    def test_invalid_missing_or_wrong_blob_are_rejected(self):
        root, git, doc, task, accepted = self.repository()
        for bad in ('merged-fixture', accepted[:12], '0' * 40):
            with self.subTest(revision=bad):
                changed = copy.deepcopy(doc)
                t = next(t for t in changed['tasks'] if t['id'] == task['id'])
                t['accepted_revision'] = bad; t['evidence'][0]['revision'] = bad
                self.assertTrue(plan.validate(changed, root))
        task['evidence'][0]['sha256'] = '0' * 64
        self.assertTrue(any('hash mismatch' in e for e in plan.validate(doc, root)))
        task['evidence'][0]['path'] = 'missing.md'
        task['evidence'][0]['sha256'] = hashlib.sha256(b'accepted evidence\n').hexdigest()
        task['write_allowlist'] = ['missing.md']; doc['tasks'][0]['write_allowlist'] = ['missing.md']
        for check in task['completion'].values():
            check['evidence'] = ['missing.md']
        self.assertTrue(any('missing from source revision' in e for e in plan.validate(doc, root)))

    def test_unrelated_archive_commit_is_rejected(self):
        root, git, doc, task, accepted = self.repository()
        tree = git('write-tree')
        unrelated = git('commit-tree', tree, '-m', 'unrelated')
        task['evidence'][0]['source_revision'] = unrelated
        self.assertTrue(any('source' in e or 'history' in e for e in plan.validate(doc, root)))

    def test_unintegrated_acceptance_commit_is_rejected(self):
        root, git, doc, task, accepted = self.repository()
        unrelated = git('commit-tree', git('write-tree'), '-m', 'unintegrated')
        task['accepted_revision'] = unrelated
        task['evidence'][0]['revision'] = unrelated
        self.assertTrue(any('history' in e for e in plan.validate(doc, root)))

    def test_unmerged_descendant_archive_is_rejected(self):
        root, git, doc, task, accepted = self.repository()
        unmerged = git('commit-tree', git('write-tree'), '-p', accepted, '-m', 'unmerged archive')
        task['evidence'][0]['source_revision'] = unmerged
        self.assertTrue(any('history' in e for e in plan.validate(doc, root)))

    def test_invalid_evidence_paths_fail_closed(self):
        root, _, doc, task, _ = self.repository()
        for bad in ('../README.md', '/tmp/README.md', 1, ['README.md']):
            with self.subTest(path=bad):
                changed = copy.deepcopy(doc)
                t = next(t for t in changed['tasks'] if t['id'] == task['id'])
                t['evidence'][0]['path'] = bad
                self.assertTrue(any('invalid evidence reference' in e for e in plan.validate(changed, root)))

    def test_evidence_pathspec_is_treated_as_a_literal(self):
        root, _, doc, task, _ = self.repository()
        path = ':(glob)README.md'
        task['evidence'][0]['path'] = path
        task['write_allowlist'] = [path]; doc['tasks'][0]['write_allowlist'] = [path]
        for check in task['completion'].values():
            check['evidence'] = [path]
        self.assertTrue(any('missing from source revision' in e for e in plan.validate(doc, root)))

    def test_symlink_and_gitlink_evidence_are_rejected(self):
        for mode in ('symlink', 'gitlink'):
            with self.subTest(mode=mode):
                root, git, doc, task, accepted = self.repository()
                if mode == 'symlink':
                    (root / 'README.md').unlink()
                    os.symlink('/etc/passwd', root / 'README.md')
                    git('add', 'README.md')
                else:
                    git('update-index', '--add', '--cacheinfo', '160000,' + accepted + ',README.md')
                git('commit', '-q', '-m', mode)
                revision = git('rev-parse', 'HEAD')
                task['accepted_revision'] = revision
                task['evidence'][0]['revision'] = revision
                self.assertTrue(any('not a regular file' in e for e in plan.validate(doc, root)))

    def test_git_environment_redirect_is_ignored(self):
        root, git, doc, task, accepted = self.repository()
        with patch.dict(os.environ, {'GIT_DIR': '/no-such-git-dir', 'GIT_WORK_TREE': '/no-such-worktree'}):
            self.assertEqual(plan.validate(doc, root), [])
