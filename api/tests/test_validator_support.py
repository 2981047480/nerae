"""Guard against false positives introduced by the offline validation adapter."""

import unittest

import yaml

from contract_support import Contract, UniqueKeyLoader


class ValidatorSupportTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.contract = Contract()

    def test_date_time_checker_is_actually_active_not_silently_optional(self):
        schema = {"type": "string", "format": "date-time"}
        self.assertTrue(self.contract.is_valid(schema, "2026-10-22T00:00:00Z"))
        self.assertFalse(self.contract.is_valid(schema, "not-a-date"))
        self.assertFalse(self.contract.is_valid(schema, "2026-02-30T00:00:00Z"))
        self.assertFalse(self.contract.is_valid(schema, "2026-10-22"))

    def test_nullable_only_extends_explicit_type_not_other_constraints(self):
        schema = {"type": "string", "nullable": True, "minLength": 1}
        self.assertTrue(self.contract.is_valid(schema, None))
        self.assertTrue(self.contract.is_valid(schema, "known"))
        self.assertFalse(self.contract.is_valid(schema, ""))
        self.assertFalse(self.contract.is_valid(schema, 3))
        with_enum = dict(schema, enum=["known"])
        self.assertFalse(self.contract.is_valid(with_enum, None), "nullable must not bypass enum")
        with_allof = {"nullable": True, "allOf": [{"type": "object"}]}
        self.assertFalse(self.contract.is_valid(with_allof, None), "nullable without a type does not override allOf")

    def test_openapi_boolean_exclusive_bounds_are_translated_correctly(self):
        exclusive = {"type": "number", "minimum": 0, "exclusiveMinimum": True}
        inclusive = {"type": "number", "minimum": 0, "exclusiveMinimum": False}
        self.assertFalse(self.contract.is_valid(exclusive, 0))
        self.assertTrue(self.contract.is_valid(exclusive, 0.5))
        self.assertTrue(self.contract.is_valid(inclusive, 0))
        maximum = {"type": "number", "maximum": 5, "exclusiveMaximum": True}
        self.assertFalse(self.contract.is_valid(maximum, 5))
        self.assertTrue(self.contract.is_valid(maximum, 4))

    def test_oneof_does_not_accidentally_degrade_to_anyof(self):
        overlapping = {"oneOf": [{"type": "integer"}, {"type": "number"}]}
        self.assertFalse(self.contract.is_valid(overlapping, 1))
        self.assertTrue(self.contract.is_valid(overlapping, 1.5))
        self.assertFalse(self.contract.is_valid(overlapping, "1"))

    def test_closed_objects_and_required_are_not_invented_or_removed(self):
        closed = {"type": "object", "properties": {"name": {"type": "string"}},
                  "required": ["name"], "additionalProperties": False}
        self.assertTrue(self.contract.is_valid(closed, {"name": "ok"}))
        self.assertFalse(self.contract.is_valid(closed, {}))
        self.assertFalse(self.contract.is_valid(closed, {"name": "ok", "admin": True}))
        open_schema = {"type": "object", "properties": {"name": {"type": "string"}}}
        self.assertTrue(self.contract.is_valid(open_schema, {}))
        self.assertTrue(self.contract.is_valid(open_schema, {"admin": True}))
        annotation_only = {"type": "object", "properties": {"secret": {"type": "string", "writeOnly": True}}}
        self.assertTrue(self.contract.is_valid(annotation_only, {"secret": "not-runtime-enforced"}))

    def test_local_ref_resolution_preserves_error_required_properties(self):
        schema = {"$ref": "#/components/schemas/Error"}
        self.assertTrue(self.contract.is_valid(schema, {"code": "ERROR", "message": "Test", "requestId": "req-1"}))
        self.assertFalse(self.contract.is_valid(schema, {"code": "ERROR", "message": "Test"}))

    def test_external_refs_are_rejected_before_any_network_retrieval(self):
        with self.assertRaisesRegex(AssertionError, "only local"):
            self.contract.validator({"$ref": "https://example.invalid/remote-schema"})

    def test_duplicate_yaml_mapping_keys_do_not_silently_overwrite_contracts(self):
        with self.assertRaisesRegex(ValueError, "duplicate YAML key"):
            yaml.load("paths:\n  /example:\n    get: {}\n    get: {}\n", Loader=UniqueKeyLoader)
