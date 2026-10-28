"""Static regression at the OpenAPI boundary; not HTTP/runtime authorization tests."""

import copy
import json
import re
import unittest
from pathlib import Path

from contract_support import Contract, walk


class ContractCase(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.contract = Contract()

    def assertValid(self, schema, value):
        errors = self.contract.errors(schema, value)
        self.assertFalse(errors, "expected valid JSON; " + "; ".join(
            "{}: {}".format(list(error.path), error.message) for error in errors[:4]))

    def assertInvalid(self, schema, value):
        self.assertTrue(self.contract.errors(schema, value), "unexpectedly accepted {!r}".format(value))

    def assertRequired(self, schema, *fields):
        self.assertTrue(set(fields) <= self.contract.required(schema),
                        "missing required fields: {}".format(set(fields) - self.contract.required(schema)))

    def assertMissingRejected(self, schema, valid, fields):
        self.assertValid(schema, valid)
        for field in fields:
            with self.subTest(missing=field):
                invalid = copy.deepcopy(valid)
                invalid.pop(field)
                self.assertInvalid(schema, invalid)

    def assertVersioned(self, path, method):
        schema = self.contract.request(path, method)
        self.assertRequired(schema, "expectedVersion")
        version = self.contract.property(schema, "expectedVersion")
        self.assertValid(version, 1)
        for invalid in (None, "1", -1):
            with self.subTest(path=path, invalidVersion=invalid):
                self.assertInvalid(version, invalid)
        statuses = {str(code) for code in self.contract.operation(path, method)["responses"]}
        self.assertTrue(statuses & {"409", "412"}, "version conflicts need explicit 409/412")


class DocumentIntegrityTests(ContractCase):
    def test_contract_is_openapi_30_and_all_references_resolve_locally(self):
        self.assertRegex(self.contract.doc["openapi"], r"^3\.0\.")
        references = [node["$ref"] for _, node in walk(self.contract.doc) if "$ref" in node]
        self.assertTrue(references)
        for ref in references:
            with self.subTest(ref=ref):
                self.assertIsInstance(self.contract.resolve(ref), dict)

    def test_component_schemas_are_valid_after_openapi_nullable_translation(self):
        schemas = self.contract.doc["components"]["schemas"]
        self.assertTrue(schemas)
        for name, schema in schemas.items():
            with self.subTest(schema=name):
                self.contract.validator(schema)

    def test_each_operation_has_a_unique_nonempty_operation_id(self):
        seen = {}
        operations = list(self.contract.operations())
        self.assertTrue(operations)
        for path, method, operation in operations:
            with self.subTest(operation=method.upper() + " " + path):
                value = operation.get("operationId")
                self.assertIsInstance(value, str)
                self.assertRegex(value, r"^[A-Za-z_][A-Za-z0-9_]*$")
                self.assertNotIn(value, seen, "duplicate operationId also used by {}".format(seen.get(value)))
                seen[value] = (path, method)

    def test_published_operation_ids_remain_stable(self):
        # Freeze the Lead's actual IDs, not a guessed naming algorithm. New paths
        # are allowed; removal/renaming of existing paths needs deliberate review.
        manifest = json.loads(Path(__file__).with_name("operation_ids.json").read_text(encoding="utf-8"))
        self.assertTrue(manifest)
        for key, expected in manifest.items():
            with self.subTest(operation=key):
                method, path = key.split(" ", 1)
                self.assertEqual(self.contract.operation(path, method).get("operationId"), expected)

    def test_all_operations_expose_the_unified_4xx_error_shape(self):
        for path, method, operation in self.contract.operations():
            with self.subTest(operation=method.upper() + " " + path):
                errors = [self.contract.deref(response) for status, response in operation["responses"].items()
                          if re.fullmatch(r"4(?:\d\d|XX)", str(status))]
                self.assertTrue(errors, "missing an explicit 4xx response")
                for response in errors:
                    schema = response.get("content", {}).get("application/json", {}).get("schema")
                    self.assertIsNotNone(schema, "4xx response must use JSON Error")
                    self.assertRequired(schema, "code", "message", "requestId")
                    valid = {"code": "VALIDATION_ERROR", "message": "Invalid input", "requestId": "req-test-1"}
                    self.assertMissingRejected(schema, valid, valid.keys())

    def test_string_enums_cannot_be_coerced_to_yaml_numbers_or_booleans(self):
        for path, node in walk(self.contract.doc):
            if node.get("type") == "string" and "enum" in node:
                with self.subTest(location="/".join(path)):
                    self.assertTrue(node["enum"])
                    self.assertTrue(all(isinstance(value, str) for value in node["enum"]),
                                    "quote all string enum values: {!r}".format(node["enum"]))
        price = next(parameter for parameter in self.contract.parameters("/plaza/coaches", "get")
                     if parameter["name"] == "priceBand")["schema"]
        for value in ("299_499", "499_799"):
            self.assertValid(price, value)
        self.assertInvalid(price, 299499)

    def test_parameter_defaults_are_inside_schema_not_parameter_object(self):
        for path, method, _ in self.contract.operations():
            for parameter in self.contract.parameters(path, method):
                with self.subTest(path=path, method=method, parameter=parameter["name"]):
                    self.assertNotIn("default", parameter)

    def test_idempotency_headers_disallow_empty_values(self):
        parameters = []
        for path, method, _ in self.contract.operations():
            parameters.extend((path, parameter) for parameter in self.contract.parameters(path, method)
                              if parameter["in"] == "header" and parameter["name"].lower() == "idempotency-key")
        self.assertTrue(parameters)
        for path, parameter in parameters:
            with self.subTest(path=path):
                minimum = parameter["schema"].get("minLength", 0)
                self.assertValid(parameter["schema"], "x" * max(14, minimum))
                self.assertInvalid(parameter["schema"], "")
                self.assertInvalid(parameter["schema"], None)
        for path in ("/me/sessions", "/publish/plans", "/me/diet-plans/{id}/checkins", "/appointments"):
            with self.subTest(criticalWrite=path):
                self.assertTrue(any(parameter["in"] == "header" and parameter["name"].lower() == "idempotency-key"
                                    for parameter in self.contract.parameters(path, "post")))


class ServiceRelationTests(ContractCase):
    def test_relation_creation_requires_identity_period_and_terms(self):
        schema = self.contract.request("/coach/relations", "post")
        self.assertRequired(schema, "studentId", "startAt", "endAt", "terms")
        self.assertInvalid(schema, {})
        self.assertInvalid(self.contract.property(schema, "studentId"), "")
        for field in ("startAt", "endAt"):
            self.assertValid(self.contract.property(schema, field), "2026-10-22T00:00:00Z")
            self.assertInvalid(self.contract.property(schema, field), "not-a-timestamp")

    def test_all_relation_confirm_and_end_actions_require_expected_version(self):
        actions = [(path, method) for path, method, _ in self.contract.operations()
                   if re.search(r"/relations/\{[^}]+\}/(?:confirm|end)$", path) and method == "post"]
        for required_path in ("/coach/relations/{id}/confirm", "/relations/{id}/confirm", "/relations/{id}/end"):
            self.contract.operation(required_path, "post")
        self.assertTrue(actions)
        for path, method in actions:
            with self.subTest(path=path):
                self.assertVersioned(path, method)
                self.assertInvalid(self.contract.request(path, method), {})

    def test_empty_scopes_are_a_versioned_revocation_not_an_implicit_regrant(self):
        path = "/relations/{id}/grants"
        self.assertVersioned(path, "put")
        schema = self.contract.request(path, "put")
        self.assertRequired(schema, "scopes", "expectedVersion")
        revoke = {"scopes": [], "expectedVersion": 2}
        self.assertMissingRejected(schema, revoke, revoke.keys())
        self.assertValid(schema, {"scopes": ["training"], "expectedVersion": 2})
        self.assertInvalid(schema, {"scopes": ["public-health"], "expectedVersion": 2})
        self.assertInvalid(schema, {"scopes": ["training"], "revoked": False})

    def test_coach_delivery_and_student_feedback_operations_exist(self):
        operations = (("/coach/relations/{id}/plans", "post"),
                      ("/coach/plans/{id}", "put"),
                      ("/coach/plans/{id}/publish", "post"),
                      ("/relations/{id}/plans", "get"),
                      ("/relations/{id}/comments", "post"),
                      ("/relations/{id}/comments", "get"))
        for path, method in operations:
            with self.subTest(path=path, method=method):
                self.contract.operation(path, method)
                if method in {"post", "put"}:
                    self.assertInvalid(self.contract.request(path, method), {})
                self.contract.response(path, method)

    def test_coach_plan_edits_and_publication_require_expected_version(self):
        for path, method in (("/coach/plans/{id}", "put"), ("/coach/plans/{id}/publish", "post")):
            with self.subTest(path=path):
                self.assertVersioned(path, method)

    def test_appointment_messages_are_a_separate_read_write_resource(self):
        path = "/appointments/{id}/messages"
        self.contract.response(path, "get")
        self.contract.response(path, "post")
        schema = self.contract.request(path, "post")
        self.assertInvalid(schema, {})
        message = self.contract.property(schema, "text", "content", "body")
        self.assertValid(message, "请问下周三可以咨询吗？")
        self.assertInvalid(message, "")


class PlanAndWorkoutTests(ContractCase):
    def test_read_training_days_expose_stable_ids(self):
        schema = self.contract.schema("TrainingDay", "TrainingDayRead", "TrainingDayResponse",
                                      predicate=lambda value: {"id", "weekIndex", "dayIndex"}
                                      <= set(self.contract.properties(value)))
        self.assertRequired(schema, "id")
        self.assertValid(self.contract.property(schema, "id"), "202")
        self.assertInvalid(self.contract.property(schema, "id"), "")
        response = self.contract.response("/me/plans/{id}")
        refs = self.contract.references(response)
        self.assertTrue(refs & self.contract.references(schema), "personal plan detail must expose read training days")

    def test_planned_workouts_pin_version_and_day_and_free_is_exclusive(self):
        schema = self.contract.request("/me/sessions", "post")
        self.assertTrue(any("oneOf" in node for _, node in self.contract.reachable(schema)),
                        "planned/free creation needs an exclusive union")
        planned = {"date": "2026-10-22", "source": "plan", "clientEventId": "workout-test-001",
                   "planId": "101", "planVersion": 2, "planDayId": "202"}
        self.assertMissingRejected(schema, planned, ("planId", "planVersion", "planDayId"))
        for value in (None, 0, "2"):
            with self.subTest(planVersion=value):
                self.assertInvalid(schema, dict(planned, planVersion=value))
        self.assertInvalid(schema, dict(planned, planDayId=""))
        free = {"date": "2026-10-22", "source": "free", "clientEventId": "workout-test-002"}
        self.assertValid(schema, free)
        self.assertInvalid(schema, dict(free, planId="101", planVersion=2, planDayId="202"))
        self.assertInvalid(schema, {"date": "2026-10-22", "source": "plan"})

    def test_personal_plan_creation_and_diet_checkins_reject_empty_objects(self):
        for path in ("/me/plans", "/coach/packages", "/me/diet-plans/{id}/checkins"):
            with self.subTest(path=path):
                self.assertInvalid(self.contract.request(path, "post"), {})

    def test_training_and_diet_content_are_structured_and_exclusive(self):
        training = self.contract.schema("TrainingContentInput", "TrainingPlanContentInput")
        diet = self.contract.schema("DietContentInput", "DietPlanContentInput")
        for name, schema in (("training", training), ("diet", diet)):
            with self.subTest(kind=name):
                self.assertInvalid(schema, {})
                self.assertTrue(self.contract.properties(schema), "content must not be arbitrary JSON")
        request = self.contract.request("/me/plans", "post")
        refs = self.contract.references(request)
        self.assertTrue(refs & self.contract.references(training))
        self.assertTrue(refs & self.contract.references(diet))
        self.assertTrue(any("oneOf" in node for _, node in self.contract.reachable(request)))

    def test_student_read_contract_documents_published_snapshot_not_coach_drafts(self):
        # Documentation/field regression only: these checks do not exercise ACLs
        # or prove that the service projects the right database snapshot.
        for path in ("/me/plans", "/me/plans/{id}", "/me/diet-plans/{id}"):
            with self.subTest(path=path):
                description = self.contract.operation(path, "get").get("description", "")
                self.assertIn("publishedVersion", description)
                self.assertRegex(description, r"未发布|草稿")
        for path in ("/me/plans/{id}", "/me/diet-plans/{id}"):
            with self.subTest(detail=path):
                schema = self.contract.response(path)
                self.assertRequired(schema, "publishedVersion")
                published = self.contract.property(schema, "publishedVersion")
                self.assertValid(published, 2)
                self.assertValid(published, None)
                self.assertInvalid(published, "2")
                self.assertInvalid(published, 0)

    def test_personal_edit_supports_diet_and_never_reuses_public_detail(self):
        candidates = [(path, method) for path, method, _ in self.contract.operations()
                      if re.fullmatch(r"/me/(?:diet-)?plans/\{[^}]+\}", path) and method in {"put", "patch"}]
        diet = self.contract.schema("DietContentInput", "DietPlanContentInput")
        diet_refs = self.contract.references(diet)
        self.assertTrue(candidates)
        self.assertTrue(any(self.contract.references(self.contract.request(path, method)) & diet_refs
                            for path, method in candidates), "no personal edit accepts diet content")
        private = self.contract.response("/me/diet-plans/{id}")
        public = self.contract.response("/plaza/plans/{id}")
        self.assertNotEqual(private, public, "personal instances must not reuse public template detail")
        public_ref = public.get("$ref")
        if public_ref:
            self.assertNotIn(public_ref, self.contract.references(private))
        self.assertRequired(private, "id", "version")


class PublicationTests(ContractCase):
    def test_manual_publish_drafts_have_create_read_edit_operations(self):
        for path, method in (("/publish/drafts", "post"), ("/publish/drafts/{id}", "get"),
                             ("/publish/drafts/{id}", "patch")):
            with self.subTest(path=path, method=method):
                self.contract.response(path, method)
                if method != "get":
                    self.assertInvalid(self.contract.request(path, method), {})

    def test_publish_accepts_only_a_versioned_draft_and_bound_confirmation(self):
        schema = self.contract.request("/publish/plans", "post")
        required = {"draftId", "expectedDraftVersion", "visibility", "confirmation"}
        self.assertEqual(set(self.contract.properties(schema)), required)
        self.assertRequired(schema, *required)
        confirmation = self.contract.property(schema, "confirmation")
        self.assertRequired(confirmation, "contentHash", "estimatedFieldPaths", "acknowledged")
        valid = {"draftId": "301", "expectedDraftVersion": 2, "visibility": "public",
                 "confirmation": {"contentHash": "a" * 64,
                                  "estimatedFieldPaths": ["/content/meals/0/foods/0/portion"],
                                  "acknowledged": True}}
        self.assertMissingRejected(schema, valid, required)
        for missing in ("contentHash", "estimatedFieldPaths", "acknowledged"):
            with self.subTest(missingConfirmation=missing):
                invalid = copy.deepcopy(valid)
                invalid["confirmation"].pop(missing)
                self.assertInvalid(schema, invalid)
        for changes in ({"draft": {}}, {"sourceTag": "official"}, {"unexpected": True}):
            with self.subTest(extra=changes):
                self.assertInvalid(schema, dict(valid, **changes))
        invalid = copy.deepcopy(valid)
        invalid["confirmation"]["acknowledged"] = False
        self.assertInvalid(schema, invalid)
        self.assertInvalid(schema, dict(valid, expectedDraftVersion=0))
        self.assertInvalid(schema, dict(valid, draftId=""))
        invalid = copy.deepcopy(valid)
        invalid["confirmation"]["contentHash"] = ""
        self.assertInvalid(schema, invalid)

    def test_publish_assist_has_traceable_ids_and_optional_structured_content(self):
        schema = self.contract.response("/publish/assist", "post")
        self.assertRequired(schema, "draftId", "draftVersion", "aiRunId")
        content = self.contract.property(schema, "content")
        # Omission is a valid empty state; do not force nullable+oneOf in OAS 3.0.
        self.assertTrue("content" not in self.contract.required(schema) or self.contract.is_valid(content, None),
                        "insufficient information must not require invented content")
        self.assertInvalid(content, {"invented": "untyped AI output"})
        self.assertTrue(any("oneOf" in node or "anyOf" in node for _, node in self.contract.reachable(content)),
                        "non-empty assisted content must use the training/diet union")
        states = self.contract.property(schema, "state", "status")
        self.assertTrue(any(value in self.contract.deref(states).get("enum", [])
                            for value in ("needs_input", "insufficient", "insufficient_data")))
        insufficient = {"draftId": "301", "draftVersion": 1, "aiRunId": "401", "state": "needs_input",
                        "fieldSuggestions": [], "missingFields": ["content.meals"], "warnings": []}
        self.assertMissingRejected(schema, insufficient, ("draftId", "draftVersion", "aiRunId"))

    def test_trusted_publication_labels_are_read_only_and_not_writable(self):
        trusted = {"sourceTag", "evidenceLevel", "certified", "authorType", "reviewLabel",
                   "trustLabel", "reviewedByProfessional"}
        found = set()
        for _, schema in self.contract.doc["components"]["schemas"].items():
            for _, node in self.contract.reachable(schema):
                for name, field in node.get("properties", {}).items():
                    if name in trusted:
                        found.add(name)
                        with self.subTest(trustedField=name):
                            self.assertTrue(self.contract.deref(field).get("readOnly"),
                                            "server-derived trust labels must be readOnly")
        self.assertTrue({"sourceTag", "evidenceLevel"} <= found)
        for path in ("/publish/plans", "/publish/drafts", "/publish/assist"):
            for _, node in self.contract.reachable(self.contract.request(path, "post")):
                for name, field in node.get("properties", {}).items():
                    if name in trusted:
                        self.assertTrue(self.contract.deref(field).get("readOnly"))


class PrivacyAndEvidenceTests(ContractCase):
    def test_injury_notes_are_write_only_and_absent_from_profile_responses(self):
        request = self.contract.request("/me/student-profile", "put")
        injury = self.contract.property(request, "injuryNotes")
        self.assertTrue(self.contract.deref(injury).get("writeOnly"))
        self.assertValid(injury, "仅用于测试的伤病说明")
        for method in ("get", "put"):
            with self.subTest(method=method):
                response = self.contract.response("/me/student-profile", method)
                for _, node in self.contract.reachable(response):
                    self.assertNotIn("injuryNotes", node.get("properties", {}),
                                     "read schemas must not contain injury raw text")
        props = self.contract.properties(request)
        for name in ("healthPrivacyLevel", "privacyLevel"):
            if name in props:
                self.assertInvalid(props[name], "public")
                self.assertInvalid(props[name], "public-health")
            else:
                self.assertInvalid(request, {"goal": "beginner", "expectedVersion": 1, name: "public"})
        for _, node in walk(self.contract.doc):
            self.assertNotIn("public-health", node.get("enum", []))

    def test_anomaly_evidence_rules_and_insufficient_data_are_explicit(self):
        schema = self.contract.schema("AnomalyFlag", "DietAnomalyFlag")
        props = self.contract.properties(schema)
        self.assertRequired(schema, "ruleVersion")
        evidence_name = next((name for name in ("evidence", "evidenceRefs", "evidenceBasis") if name in props), None)
        self.assertIsNotNone(evidence_name, "anomaly assessment needs observed evidence")
        self.assertRequired(schema, evidence_name)
        self.assertTrue(any("insufficient_data" in node.get("enum", [])
                            for _, node in self.contract.reachable(schema)),
                        "insufficient observations need an explicit insufficient_data result")
        self.assertInvalid(self.contract.property(schema, "ruleVersion"), "")
        self.assertValid(self.contract.property(schema, "ruleVersion"), "diet-observation-v1")

    def test_diet_self_report_is_not_accepted_as_fabricated_intake_evidence(self):
        schema = self.contract.request("/me/diet-plans/{id}/checkins", "post")
        self.assertRequired(schema, "date")
        # The honest self-report remains useful without inventing calories or foods.
        honest = {"date": "2026-10-22", "expectedVersion": 0, "planVersion": 1, "dayIndex": 1,
                  "mealStatuses": [{"slotKey": "breakfast", "status": "followed"}]}
        self.assertMissingRejected(schema, honest, honest.keys())
        self.assertInvalid(schema, dict(honest, mealStatuses=[]))
        self.assertInvalid(schema, dict(honest, actualIntakeKcal=1800))
        observed = dict(honest, observations=[{"description": "自述一碗米饭，份量未知",
                                              "source": "self_reported", "confirmed": True}])
        self.assertValid(schema, observed)
        self.assertInvalid(schema, dict(honest, observations=[{"description": "未经确认的AI结果"}]))
        descriptions = " ".join(str(node.get("description", "")) for _, node in self.contract.reachable(schema))
        # These documentation assertions are NOT a claim that a backend enforces them.
        self.assertRegex(descriptions, r"自评|自述|自报|self.report|self.assess")
        self.assertRegex(descriptions, r"不.{0,15}(实际|真实).{0,8}(摄入|热量)|不等同测量事实|not.{0,30}(actual|observed).{0,12}intake")


if __name__ == "__main__":
    unittest.main(verbosity=2)
