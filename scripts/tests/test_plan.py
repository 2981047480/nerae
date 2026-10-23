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
        self.assertEqual(plan.ready_tasks(tasks, self.doc['deferred_packages']), ['D01-01','F01-01','L01-01'])
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
        self.assertEqual(plan.ready_tasks(tasks, {'F01':'test hold'}),['D01-01','L01-01'])

    def test_blocked_work_package_stops_its_ready_children(self):
        self.task('F01')['status']='blocked'
        tasks={t['id']:t for t in self.doc['tasks']}
        self.assertEqual(plan.ready_tasks(tasks,self.doc['deferred_packages']),['D01-01','L01-01'])

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
