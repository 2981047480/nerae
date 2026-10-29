"""Recording wire-contract examples; these do not exercise an HTTP server."""

from test_static_contract import ContractCase


class RecordingContractTests(ContractCase):
    def test_recording_kinds_carry_only_their_actual_measurements(self):
        schema = self.contract.schema('SetRecord')
        base = {'exerciseId': '101', 'exerciseEntryKey': 'exercise-test-001',
                'setIndex': 1, 'recordKey': 'set-test-001'}
        for kind in ('weight', 'perSide'):
            self.assertValid(schema, dict(base, kind=kind, weightKg=12.5, reps=8))
            self.assertInvalid(schema, dict(base, kind=kind, reps=8))
            self.assertInvalid(schema, dict(base, kind=kind, weightKg=12.5))
            self.assertInvalid(schema, dict(base, kind=kind, weightKg=12.5, reps=8, seconds=40))
        for kind in ('bodyweight', 'bodyweightPerSide', 'breath'):
            self.assertValid(schema, dict(base, kind=kind, reps=8))
            self.assertInvalid(schema, dict(base, kind=kind))
            self.assertInvalid(schema, dict(base, kind=kind, reps=8, weightKg=12.5))
            self.assertInvalid(schema, dict(base, kind=kind, reps=8, seconds=40))
        for kind in ('duration', 'durationPerSide'):
            self.assertValid(schema, dict(base, kind=kind, seconds=40))
            self.assertInvalid(schema, dict(base, kind=kind))
            self.assertInvalid(schema, dict(base, kind=kind, seconds=40, reps=8))
            self.assertInvalid(schema, dict(base, kind=kind, seconds=40, weightKg=10))

    def test_blank_partial_and_invalid_sets_are_not_recorded(self):
        schema = self.contract.schema('SetRecord')
        base = {'exerciseId': '101', 'exerciseEntryKey': 'exercise-test-001',
                'setIndex': 1, 'recordKey': 'set-test-001'}
        for sample in (
            base,
            dict(base, kind='weight', weightKg=10),
            dict(base, kind='weight', weightKg=0, reps=1),
            dict(base, kind='weight', weightKg=301, reps=1),
            dict(base, kind='bodyweight', reps=0),
            dict(base, kind='bodyweight', reps=101),
            dict(base, kind='bodyweight', reps=1.5),
            dict(base, kind='duration', seconds=4),
            dict(base, kind='duration', seconds=601),
            dict(base, kind='duration', seconds=20.5),
            dict(base, kind='duration', seconds=None),
            dict(base, kind='bodyweight', reps=8, completed=False),
            dict(base, kind='unknown', reps=8),
            dict(base, kind='bodyweight', reps=8, recordKey='short'),
            dict(base, kind='bodyweight', reps=8, inventedAt='2026-10-23T10:00:00Z'),
        ):
            with self.subTest(sample=sample):
                self.assertInvalid(schema, sample)

    def test_set_batch_supports_explicit_upserts_and_deletes_with_version(self):
        path = '/me/sessions/{id}/sets'
        schema = self.contract.request(path, 'post')
        row = {'exerciseId': '101', 'exerciseEntryKey': 'exercise-test-001',
               'setIndex': 1, 'recordKey': 'set-test-001',
               'kind': 'weight', 'weightKg': 12.5, 'reps': 8}
        order = ['exercise-test-001']
        self.assertValid(schema, {'expectedVersion': 1, 'exerciseEntryKeys': order, 'sets': [row]})
        self.assertValid(schema, {'expectedVersion': 2, 'exerciseEntryKeys': [], 'deleteRecordKeys': ['set-test-001']})
        self.assertValid(schema, {'expectedVersion': 3, 'exerciseEntryKeys': order, 'sets': [row], 'deleteRecordKeys': ['set-test-002']})
        self.assertValid(schema, {'expectedVersion': 4, 'exerciseEntryKeys': order, 'sets': [row], 'restoreRecordKeys': ['set-test-001']})
        self.assertValid(schema, {'expectedVersion': 5, 'exerciseEntryKeys': order + ['exercise-test-002'],
                                  'sets': [row, dict(row, recordKey='set-test-002', exerciseEntryKey='exercise-test-002')]})
        self.assertValid(schema, {'expectedVersion': 6, 'exerciseEntryKeys': order})
        self.assertValid(schema, {'expectedVersion': 7, 'exerciseEntryKeys': [f'entry-{n:04}' for n in range(2000)]})
        for sample in ({}, {'expectedVersion': 1}, {'expectedVersion': 0, 'sets': [row]},
                       {'expectedVersion': 1, 'sets': [row]},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order, 'sets': []},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order, 'deleteRecordKeys': []},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order, 'restoreRecordKeys': ['set-test-001']},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order, 'deleteRecordKeys': ['set-test-002'],
                        'restoreRecordKeys': ['set-test-001']},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order, 'deleteRecordKeys': ['set-test-001', 'set-test-001']},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order, 'sets': [row] * 201},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order, 'sets': [dict(row, resolvedExerciseName='伪造名称')]},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order, 'sets': [row], 'unexpected': True},
                       {'expectedVersion': 1, 'exerciseEntryKeys': order * 2},
                       {'expectedVersion': 1, 'exerciseEntryKeys': ['short']},
                       {'expectedVersion': 1, 'exerciseEntryKeys': [f'entry-{n:04}' for n in range(2001)]}):
            with self.subTest(sample=sample):
                self.assertInvalid(schema, sample)
        self.assertIn('412', self.contract.operation(path, 'post')['responses'])
        self.assertTrue(any(p['name'] == 'Idempotency-Key' for p in self.contract.parameters(path, 'post')))

    def test_library_and_user_named_exercises_have_distinct_stable_entries(self):
        schema = self.contract.schema('SetRecord')
        base = {'exerciseEntryKey': 'exercise-custom-001', 'setIndex': 1,
                'recordKey': 'set-custom-001', 'kind': 'bodyweight', 'reps': 8}
        self.assertValid(schema, dict(base, exerciseName='自定义动作'))
        self.assertValid(schema, dict(base, exerciseId='101'))
        for sample in (base, dict(base, exerciseId='101', exerciseName='自定义动作'),
                       dict(base, exerciseName='   '), dict(base, exerciseName='x' * 61),
                       dict(base, exerciseId='not-an-id'),
                       {k: v for k, v in dict(base, exerciseName='自定义动作').items()
                        if k != 'exerciseEntryKey'}):
            with self.subTest(sample=sample):
                self.assertInvalid(schema, sample)
        self.assertTrue(self.contract.property(schema, 'resolvedExerciseName').get('readOnly'))

    def test_save_supports_unknown_duration_and_explicit_date_revision(self):
        path = '/me/sessions/{id}/finish'
        schema = self.contract.request(path, 'post')
        for sample in ({'expectedVersion': 1}, {'expectedVersion': 2, 'durationMin': None,
                                               'rpe': None, 'note': None},
                       {'expectedVersion': 3, 'date': '2026-10-20', 'durationMin': 30}):
            self.assertValid(schema, sample)
        for sample in ({}, {'expectedVersion': 0}, {'expectedVersion': 1, 'durationMin': -1},
                       {'expectedVersion': 1, 'date': '2026-02-30'},
                       {'expectedVersion': 1, 'completedAt': '2026-10-20T10:00:00Z'}):
            with self.subTest(sample=sample):
                self.assertInvalid(schema, sample)
        self.assertIn('412', self.contract.operation(path, 'post')['responses'])

    def test_saved_record_can_be_reloaded_with_a_date_and_server_times(self):
        path = '/me/sessions/{id}'
        response = self.contract.response(path, 'get')
        row = {'exerciseId': '101', 'exerciseEntryKey': 'exercise-test-001',
               'setIndex': 1, 'recordKey': 'set-test-001', 'kind': 'bodyweight', 'reps': 8}
        valid = {'id': '101', 'date': '2026-10-20', 'timeZone': 'Asia/Shanghai',
                 'status': 'completed', 'source': 'free', 'version': 3,
                 'createdAt': '2026-10-23T10:00:00Z', 'updatedAt': '2026-10-23T10:03:00Z',
                 'recordedAt': '2026-10-23T10:03:00Z',
                 'sets': [dict(row, resolvedExerciseName='俯卧撑')], 'exerciseEntryKeys': ['exercise-test-001']}
        self.assertMissingRejected(response, valid, ('id', 'date', 'timeZone', 'status', 'source',
                                                     'version', 'createdAt', 'updatedAt', 'recordedAt', 'sets', 'exerciseEntryKeys'))
        self.assertInvalid(response, dict(valid, recordedAt=None))
        self.assertInvalid(response, dict(valid, recordedAt='2026-10-20'))
        self.assertInvalid(response, dict(valid, sets=[]))
        self.assertValid(response, dict(valid, status='in_progress', recordedAt=None, sets=[], exerciseEntryKeys=[]))
        self.assertValid(response, dict(valid, status='skipped', recordedAt=None, sets=[], exerciseEntryKeys=[]))
        self.assertValid(response, dict(valid, status='skipped', sets=[], exerciseEntryKeys=[]))
        self.assertInvalid(response, dict(valid, status='in_progress', recordedAt=None, sets=[]))
        self.assertInvalid(response, dict(valid, exerciseEntryKeys=[]))
        self.assertInvalid(response, dict(valid, exerciseEntryKeys=['exercise-test-001'] * 2))
        self.assertInvalid(response, dict(valid, status='skipped', recordedAt=None))
        self.assertInvalid(response, dict(valid, status='skipped'))
        summary = self.contract.schema('WorkoutSession')
        self.assertInvalid(summary, dict(valid, status='skipped'))
        self.assertValid(summary, {k: v for k, v in dict(valid, status='skipped').items() if k != 'sets'})
        self.assertValid(response, dict(valid, rpe=7.5, durationMin=None))
        self.assertInvalid(response, dict(valid, rpe=11))
        self.assertInvalid(response, dict(valid, durationMin=-1))
        self.assertInvalid(response, dict(valid, sets=[row]))
