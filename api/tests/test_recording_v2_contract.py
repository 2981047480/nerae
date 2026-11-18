"""New recording contract examples; does not itself prove an HTTP/DB flow."""
from test_static_contract import ContractCase


class RecordingV2ContractTests(ContractCase):
    def test_blank_partial_and_zero_are_distinct(self):
        schema = self.contract.schema('RecordingV2Set')
        base = {'recordKey': 'record-001', 'exerciseEntryKey': 'entry-0001', 'setIndex': 1, 'confirmed': False}
        for measurement in ({'method': 'total'}, {'method': 'total', 'weightKg': '45'},
                            {'method': 'total', 'reps': 8}, {'method': 'total', 'weightKg': '0', 'reps': 8}):
            self.assertValid(schema, dict(base, measurement=measurement))
        for measurement in ({'method': 'total', 'weightKg': 0}, {'method': 'total', 'weightKg': '-1'},
                            {'method': 'total', 'weightKg': '1.234'}, {'method': 'total', 'reps': 0},
                            {'method': 'duration', 'seconds': 30}, {'method': 'total', 'invented': True}):
            self.assertInvalid(schema, dict(base, measurement=measurement))

    def test_multiple_measurement_methods_and_sides_are_explicit(self):
        schema = self.contract.schema('RecordingV2Measurement')
        for measurement in ({'method': 'pair_each', 'weightKg': '12', 'reps': 10},
                            {'method': 'sides', 'left': {'state': 'performed', 'weightKg': '12', 'reps': 10},
                             'right': {'state': 'not_performed'}},
                            {'method': 'bodyweight_extra', 'bodyweightKg': '70', 'adjustmentKg': '10', 'reps': 10},
                            {'method': 'bodyweight_assisted', 'bodyweightKg': '70', 'adjustmentKg': '20', 'reps': 10},
                            {'method': 'direct', 'weightKg': '50', 'reps': 10}):
            self.assertValid(schema, measurement)
        self.assertInvalid(schema, {'method': 'sides', 'left': {'state': 'done'}})
        self.assertInvalid(schema, {'method': 'total', 'weightKg': '12kg'})

    def test_batch_retains_identity_and_requires_version_and_order(self):
        schema = self.contract.schema('RecordingV2Batch')
        base = {'expectedVersion': 1, 'exerciseEntryKeys': ['entry-0001'],
                'addEntries': [{'exerciseEntryKey': 'entry-0001', 'exerciseName': '深蹲'}],
                'sets': [{'recordKey': 'record-001', 'exerciseEntryKey': 'entry-0001', 'setIndex': 1,
                          'confirmed': False, 'measurement': {'method': 'total'}}]}
        self.assertValid(schema, base)
        self.assertValid(schema, {'expectedVersion': 2, 'exerciseEntryKeys': ['entry-0001'], 'removeEntryKeys': ['entry-0001']})
        self.assertValid(schema, dict(base, removeRecords=[{'recordKey': 'record-001', 'removedPosition': 2}]))
        self.assertInvalid(schema, dict(base, expectedVersion=0))
        self.assertInvalid(schema, dict(base, expectedVersion=9007199254740992))
        self.assertInvalid(schema, dict(base, exerciseEntryKeys=['entry-0001', 'entry-0001']))
        self.assertInvalid(schema, dict(base, unexpected=True))
        self.assertInvalid(schema, {'expectedVersion': 1})

    def test_batch_accepts_valid_training_date_without_accepting_invalid_dates(self):
        schema = self.contract.schema('RecordingV2Batch')
        self.assertValid(schema, {'expectedVersion': 2, 'exerciseEntryKeys': [], 'date': '2026-11-02'})
        self.assertInvalid(schema, {'expectedVersion': 2, 'exerciseEntryKeys': [], 'date': '2026-02-30'})
        self.assertInvalid(schema, {'expectedVersion': 2, 'exerciseEntryKeys': [], 'date': None})

    def test_replay_uses_original_request_and_success_marker(self):
        for original, replay, status in (
                ('/me/recordings', '/me/recordings/replay', '201'),
                ('/me/recordings/{id}/sets', '/me/recordings/{id}/sets/replay', '200'),
                ('/me/recordings/{id}/finish', '/me/recordings/{id}/finish/replay', '200')):
            self.assertEqual(self.contract.request(original, 'post'), self.contract.request(replay, 'post'))
            responses = self.contract.operation(replay, 'post')['responses']
            self.assertTrue(self.contract.deref(responses[status])['headers']['Idempotency-Replayed']['required'])
            self.assertIn('503', responses)

    def test_volume_preserves_units_and_missing_contributions(self):
        schema = self.contract.schema('RecordingV2Volume')
        self.assertValid(schema, {'calculatedKg': '284.00', 'fullSets': 2, 'partialSets': 0, 'excludedSets': 1})
        self.assertValid(schema, {'fullSets': 0, 'partialSets': 0, 'excludedSets': 1})
        self.assertValid(schema, {'calculatedKg': '0.00', 'fullSets': 1, 'partialSets': 0, 'excludedSets': 0})
        self.assertInvalid(schema, {'calculatedKg': '284 kg', 'fullSets': 2, 'partialSets': 0, 'excludedSets': 0})
        self.assertInvalid(schema, {'calculatedKg': '-1', 'fullSets': 2, 'partialSets': 0, 'excludedSets': 0})
        self.assertInvalid(schema, {'fullSets': 1, 'partialSets': 0, 'excludedSets': -1})
        self.assertInvalid(schema, {'fullSets': 1, 'partialSets': 0})

    def test_action_total_identifies_each_segment_once(self):
        schema = self.contract.schema('RecordingV2ActionVolume')
        volume = {'calculatedKg': '620.00', 'fullSets': 2, 'partialSets': 0, 'excludedSets': 0}
        self.assertValid(schema, {'exerciseEntryKeys': ['entry-0001', 'entry-0002'], 'volume': volume})
        self.assertInvalid(schema, {'exerciseEntryKeys': [], 'volume': volume})
        self.assertInvalid(schema, {'exerciseEntryKeys': ['entry-0001', 'entry-0001'], 'volume': volume})
        self.assertInvalid(schema, {'exerciseEntryKeys': ['entry-0001'], 'volume': volume, 'unexpected': True})
