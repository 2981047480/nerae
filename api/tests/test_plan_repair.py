"""R01-R05/R08/R09 regression: schema evidence, not backend behavior."""
import copy
from examples import training_content, training_read_content
from test_static_contract import ContractCase


class PlanRepairTests(ContractCase):
    def test_read_exercise_requires_frozen_description_but_writes_reject_it(self):
        valid = training_read_content()['days'][0]['items'][0]
        self.assertMissingRejected(self.contract.schema('Prescription'), valid, ('exerciseSnapshot',))
        self.assertInvalid(self.contract.schema('PrescriptionInput'), valid)
        for field in ('name', 'version', 'cues', 'precautions', 'alternatives'):
            broken = copy.deepcopy(valid)
            broken['exerciseSnapshot'].pop(field)
            self.assertInvalid(self.contract.schema('Prescription'), broken)

    def test_catalog_list_and_versioned_detail_have_typed_responses(self):
        self.assertValid(self.contract.response('/exercises'), {'items': [], 'nextCursor': None})
        self.contract.property(self.contract.response('/exercises/{id}'), 'snapshot')
        parameters = self.contract.parameters('/exercises/{id}', 'get')
        self.assertTrue(any(p['name'] == 'version' for p in parameters))

    def test_coach_can_reload_draft_with_head_and_published_version(self):
        detail = self.contract.response('/coach/plans/{id}')
        self.assertRequired(detail, 'plan', 'editable', 'editState')
        plan = self.contract.property(detail, 'plan')
        self.assertRequired(plan, 'version', 'publishedVersion', 'content')
        self.assertValid(self.contract.response('/coach/relations/{id}/plans'), {'items': [], 'nextCursor': None})
        self.assertEqual(self.contract.operation('/coach/plans/{id}', 'get')['x-required-role'], 'approved_coach')

    def test_coach_student_detail_rejects_arbitrary_or_empty_health_json(self):
        schema = self.contract.response('/coach/students/{relationId}')
        self.assertRequired(schema, 'relation', 'grant', 'profile', 'sections')
        self.assertInvalid(schema, {})
        self.assertInvalid(schema, {'injuryNotes': 'unexpected'})
        section = self.contract.schema('StudentProfileSection')
        self.assertValid(section, {'state': 'withheld'})
        self.assertInvalid(section, {'state': 'withheld', 'value': {'goal': 'beginner', 'version': 1}})
        self.assertInvalid(self.contract.schema('SharedStudentProfile'), {'goal': 'beginner', 'version': 1, 'hasInjuryNotes': True})

    def test_ai_ready_requires_persisted_complete_read_content(self):
        schema = self.contract.response('/coach/ai/plan-draft', 'post')
        valid = {'state':'ready','planId':'101','planVersion':1,'aiRunId':'301','content':training_read_content(),'warnings':[]}
        self.assertMissingRejected(schema, valid, valid.keys())
        self.assertInvalid(schema, dict(valid, content=training_content()))
        self.assertInvalid(schema, dict(valid, content={}))

    def test_ai_nonready_does_not_manufacture_a_plan(self):
        schema = self.contract.schema('CoachAiDraftResponse')
        for state,field in [('needs_input','missingFields'),('safety_block','reasons')]:
            valid = {'state':state,'aiRunId':'301',field:['test-only'],'warnings':[]}
            self.assertValid(schema, valid)
            self.assertInvalid(schema, dict(valid, **{field:[]}))
            for name,value in [('planId','101'),('planVersion',1),('content',training_read_content())]:
                self.assertInvalid(schema, dict(valid, **{name:value}))

    def test_ai_technical_failure_is_not_a_valid_success_state(self):
        schema = self.contract.schema('CoachAiDraftResponse')
        self.assertInvalid(schema, {'state':'technical_failure','aiRunId':'301','warnings':[]})
        self.assertIn('503', self.contract.operation('/coach/ai/plan-draft','post')['responses'])

    def test_schedule_has_separate_version_anchor_timezone_and_effective_date(self):
        schema = self.contract.request('/me/training-schedules','post')
        valid = {'planId':'101','planVersion':2,'anchorDate':'2026-10-22','effectiveFrom':'2026-10-23','timeZone':'Asia/Shanghai','expectedSelectionVersion':0,'replaceCurrent':False}
        self.assertMissingRejected(schema, valid, valid.keys())
        self.assertInvalid(schema, dict(valid, anchorDate='2026-02-30'))
        self.assertInvalid(schema, dict(valid, timeZone=''))
        self.assertInvalid(schema, dict(valid, expectedSelectionVersion=-1))

    def test_no_current_schedule_does_not_require_a_fake_schedule(self):
        schema = self.contract.response('/me/training-schedule')
        self.assertValid(schema, {'state':'none','selectionVersion':3})
        self.assertInvalid(schema, {'state':'selected','selectionVersion':3})
        self.assertInvalid(schema, {'state':'none','selectionVersion':3,'schedule':{}})

    def test_schedule_commands_cannot_edit_frozen_version_or_timezone(self):
        schema = self.contract.request('/me/training-schedules/{id}','patch')
        for action in ['pause','resume','end']:
            self.assertValid(schema, {'action':action,'expectedVersion':2})
        self.assertInvalid(schema, {'action':'resume','expectedVersion':2,'timeZone':'UTC'})
        self.assertInvalid(schema, {'action':'resume','expectedVersion':2,'planVersion':3})

    def test_reschedule_requires_occurrence_and_schedule_cas(self):
        schema = self.contract.request('/me/training-occurrences/{id}','patch')
        valid = {'action':'move','expectedVersion':1,'expectedScheduleVersion':2,'scheduledDate':'2026-10-25'}
        self.assertMissingRejected(schema, valid, valid.keys())
        self.assertInvalid(schema, dict(valid, action='skip'))
        self.assertValid(schema, {'action':'skip','expectedVersion':1,'expectedScheduleVersion':2})

    def test_today_empty_state_is_a_typed_result(self):
        valid = {'date':'2026-10-23','timeZone':'Asia/Shanghai','selectionVersion':0,'state':'unconfigured','occurrences':[]}
        self.assertMissingRejected(self.contract.response('/me/today-training'), valid, valid.keys())

    def test_atomic_start_is_exclusive_and_deduplicated_by_client_event(self):
        schema = self.contract.request('/me/training-starts','post')
        valid = {'source':'template','templateId':'101','templateVersion':2,'templateDayId':'202','date':'2026-10-23','timeZone':'Asia/Shanghai','expectedSelectionVersion':0,'replaceCurrent':False,'clientEventId':'start-test-001'}
        self.assertMissingRejected(schema, valid, valid.keys())
        self.assertInvalid(schema, dict(valid, planId='101',planVersion=1,planDayId='202'))
        self.assertTrue(any(p['name']=='Idempotency-Key' for p in self.contract.parameters('/me/training-starts','post')))

    def test_occurrence_is_allowed_only_on_planned_sessions(self):
        schema = self.contract.request('/me/sessions','post')
        common = {'date':'2026-10-23','clientEventId':'workout-test-001','occurrenceId':'601'}
        self.assertValid(schema, dict(common, source='plan',planId='101',planVersion=2,planDayId='202'))
        self.assertInvalid(schema, dict(common, source='free'))

    def test_relationship_message_has_no_appointment_or_plan_prerequisite(self):
        schema = self.contract.request('/relations/{id}/messages','post')
        valid = {'text':'请问方便沟通吗？','contentClass':'ordinary','scopes':[]}
        self.assertValid(schema, valid)
        for key in ('appointmentId','planId','senderId'):
            self.assertInvalid(schema, dict(valid, **{key:'101'}))
        self.assertInvalid(schema, dict(valid, text=''))
        self.assertInvalid(schema, dict(valid, contentClass='health'))
        self.assertInvalid(schema, dict(valid, scopes=['training']))
        self.assertValid(schema, dict(valid, contentClass='health', scopes=['training']))

    def test_withheld_message_cannot_contain_health_text_or_preview(self):
        schema = self.contract.schema('RelationMessage')
        valid = {'id':'801','relationId':'701','createdAt':'2026-10-23T10:00:00Z','visibility':'withheld','reason':'grant_revoked'}
        self.assertValid(schema, valid)
        for key in ['text','preview','senderId']:
            self.assertInvalid(schema, dict(valid, **{key:'hidden'}))

    def test_conversation_read_position_is_explicit_not_push_delivery(self):
        schema = self.contract.request('/relations/{id}/messages/read','post')
        self.assertValid(schema, {'lastReadMessageId':'801'})
        self.assertInvalid(schema, {'pushDelivered':True})

    def test_adjustment_confirmation_binds_actual_base_hash_and_acknowledgment(self):
        schema = self.contract.request('/me/plan-adjustments/{id}/apply','post')
        valid = {'expectedVersion':2,'proposalHash':'a'*64,'acknowledged':True}
        self.assertMissingRejected(schema, valid, valid.keys())
        self.assertInvalid(schema, dict(valid, acknowledged=False))
        self.assertInvalid(schema, dict(valid, content=training_content()))

    def test_adjustment_ready_requires_complete_content_and_applied_is_separate(self):
        schema = self.contract.schema('PlanAdjustment')
        common = {'id':'901','planId':'101','baseVersion':2,'aiRunId':'301','expiresAt':'2026-10-24T10:00:00Z','warnings':[]}
        ready = dict(common,state='ready',content=training_content(),proposalHash='b'*64,changes=['测试差异'])
        self.assertMissingRejected(schema, ready, ('content','proposalHash','changes'))
        self.assertValid(schema, dict(common,state='applied',appliedVersion=3,proposalHash='b'*64))
        self.assertInvalid(schema, dict(ready,state='applied',appliedVersion=3))
