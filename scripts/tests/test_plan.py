# -*- coding: utf-8 -*-
import copy
import importlib.util
import json
import hashlib
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

    def test_parallel_owner_collision_rejected(self):
        task = next(t for t in self.doc['tasks'] if t['id']=='D01')
        task['write_allowlist'].append('apps/api/cmd/subpath/')
        self.assertTrue(any('write overlap' in e for e in plan.validate(self.doc)))

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
        task.update(status='accepted',owner='test',actual_model='test',base_revision='test',reviewed_by='test',accepted_revision='test',evidence=[{'path':'README.md','sha256':'0'*64}])
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
        child.update(kind=node_kind, depends_on=[], write_allowlist=['README.md'], inputs=['README.md'], status='accepted', owner='worker-fixture', actual_model='model-fixture', actual_reasoning='medium', base_revision='base-fixture', accepted_revision='merged-fixture', reviewed_by='reviewer-fixture', review_mode='independent')
        child['evidence']=[{'path':'README.md','sha256':hashlib.sha256((ROOT/'README.md').read_bytes()).hexdigest(),'revision':'merged-fixture'}]
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
        # Even if foundation is ready, contract generation stays blocked until
        # the record, identity and deletion contract package is accepted.
        tasks['F01']['status']='accepted'
        self.assertNotIn('F02-01', plan.ready_tasks(tasks, self.doc['deferred_packages']))
        tasks['K01']['status']='accepted'
        self.assertIn('F02-01', plan.ready_tasks(tasks, self.doc['deferred_packages']))
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
        ids=['F01','K01','D02','F02','P02-01','P02-02','P02-03','L01-01',
             'A01','A02','I-A01','A03','A04','S01-A','I-A02','U01','U02','I-A04']
        self.assertEqual(len(ids),len(set(ids)))
        self.assertEqual([sum(tasks[k]['effort_days'][i] for k in ids) for i in (0,1)],[49,95])
        self.assertIn('**49–95**',(ROOT/'docs/IMPLEMENTATION_PLAN.md').read_text())

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
