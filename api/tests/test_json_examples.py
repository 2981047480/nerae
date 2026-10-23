"""Literal JSON positive/negative examples at the public contract boundary."""

import copy

from contract_support import canonical_path
from examples import diet_content, service_terms, training_content, training_read_content
from test_static_contract import ContractCase


class ContentExampleTests(ContractCase):
    def test_training_content_accepts_prescriptions_and_rejects_incomplete_or_wrong_kind(self):
        schema = self.contract.schema("TrainingContentInput", "TrainingPlanContentInput")
        valid = training_content()
        self.assertMissingRejected(schema, valid, ("kind", "weeks", "days"))
        self.assertInvalid(schema, dict(valid, kind="diet"))
        self.assertInvalid(schema, dict(valid, days=[]))
        self.assertInvalid(schema, dict(valid, meals=diet_content()["meals"]))
        self.assertInvalid(schema, dict(valid, weeks="2"))
        incomplete = copy.deepcopy(valid)
        incomplete["days"][0]["items"][0].pop("exerciseId")
        self.assertInvalid(schema, incomplete)
        incomplete = copy.deepcopy(valid)
        incomplete["days"][0]["items"][0]["sets"] = 0
        self.assertInvalid(schema, incomplete)
        forged_read_id = copy.deepcopy(valid)
        forged_read_id["days"][0]["id"] = "202"
        self.assertInvalid(schema, forged_read_id)

    def test_diet_content_keeps_unknown_allergens_and_rejects_missing_portions(self):
        schema = self.contract.schema("DietContentInput", "DietPlanContentInput")
        valid = diet_content()
        self.assertMissingRejected(schema, valid, ("kind", "nutritionBand", "meals", "allergenAssessment"))
        self.assertInvalid(schema, dict(valid, kind="training"))
        self.assertInvalid(schema, dict(valid, days=training_content()["days"]))
        self.assertInvalid(schema, dict(valid, meals=[]))
        incomplete = copy.deepcopy(valid)
        incomplete["meals"][0]["foods"][0].pop("quantity")
        self.assertInvalid(schema, incomplete)
        invalid = copy.deepcopy(valid)
        invalid["meals"][0]["foods"][0]["quantity"]["value"] = 0
        self.assertInvalid(schema, invalid)
        invalid = copy.deepcopy(valid)
        invalid["meals"][0]["foods"][0]["quantity"]["unit"] = "unreviewed_unit"
        self.assertInvalid(schema, invalid)
        invalid = copy.deepcopy(valid)
        invalid["allergenAssessment"]["status"] = "guaranteed_safe"
        self.assertInvalid(schema, invalid)

    def test_plan_content_union_accepts_exactly_one_training_or_diet_branch(self):
        create = self.contract.request("/me/plans", "post")
        union = self.contract.property(create, "content")
        self.assertValid(union, training_content())
        self.assertValid(union, diet_content())
        self.assertInvalid(union, {})
        self.assertInvalid(union, None)
        self.assertInvalid(union, dict(training_content(), **{"kind": "unknown"}))
        mixed = training_content()
        mixed.update(diet_content())
        self.assertInvalid(union, mixed)

    def test_personal_plan_creation_and_edit_accept_both_content_types(self):
        create = self.contract.request("/me/plans", "post")
        candidates = [(path, method) for path, method, _ in self.contract.operations()
                      if canonical_path(path) in {"/me/plans/{}", "/me/diet-plans/{}"} and method in {"put", "patch"}]
        self.assertTrue(candidates)
        for factory in (training_content, diet_content):
            content = factory()
            with self.subTest(kind=content["kind"]):
                valid = {"title": "我的测试计划", "goal": "beginner", "planType": content["kind"], "content": content}
                self.assertMissingRejected(create, valid, ("title", "planType", "content"))
                self.assertInvalid(create, dict(valid, sourceTag="official"))
                self.assertInvalid(create, dict(valid, ownerId="999"))
        for path, method in candidates:
            with self.subTest(editPath=path):
                edit = self.contract.request(path, method)
                valid = {"title": "更新的测试饮食计划", "expectedVersion": 2, "content": diet_content()}
                self.assertMissingRejected(edit, valid, ("title", "expectedVersion", "content"))
                self.assertInvalid(edit, dict(valid, expectedVersion=0))

    def test_read_training_day_requires_id_but_input_does_not_allow_it(self):
        read = self.contract.schema("TrainingDay", "TrainingDayRead", "TrainingDayResponse")
        day = training_content()["days"][0]
        valid = training_read_content()["days"][0]
        self.assertMissingRejected(read, valid, ("id",))
        self.assertTrue(self.contract.deref(self.contract.property(read, "id")).get("readOnly"))
        input_schema = self.contract.schema("TrainingDayInput", "TrainingDayWrite")
        self.assertValid(input_schema, day)
        self.assertInvalid(input_schema, valid)


class RelationshipExampleTests(ContractCase):
    def test_service_relation_requires_real_terms_not_empty_shells(self):
        schema = self.contract.request("/coach/relations", "post")
        valid = {"studentId": "101", "startAt": "2026-10-22T00:00:00Z",
                 "endAt": "2026-11-19T00:00:00Z", "terms": service_terms()}
        self.assertMissingRejected(schema, valid, valid.keys())
        self.assertInvalid(schema, dict(valid, terms={}))
        self.assertInvalid(schema, dict(valid, startAt="2026-02-30T00:00:00Z"))
        self.assertInvalid(schema, dict(valid, coachId="999"))
        self.assertInvalid(schema, dict(valid, status="active"))
        self.assertInvalid(schema, dict(valid, paymentVerified=True))

    def test_service_package_creation_has_complete_literal_positive_and_negative_examples(self):
        schema = self.contract.request("/coach/packages", "post")
        valid = dict(service_terms(), templateType="monthly", durationDays=28, hasVideo=False)
        self.assertMissingRejected(schema, valid, valid.keys())
        for changes in ({"priceCents": -1}, {"durationDays": 0}, {"includes": []}, {"hasVideo": "false"},
                        {"certified": True}, {"sourceTag": "official"}):
            with self.subTest(changes=changes):
                self.assertInvalid(schema, dict(valid, **changes))

    def test_relation_confirmation_binds_version_and_terms_without_implicit_acceptance(self):
        valid = {"expectedVersion": 2, "termsHash": "b" * 64, "acknowledged": True}
        for path in ("/coach/relations/{id}/confirm", "/relations/{id}/confirm"):
            with self.subTest(path=path):
                schema = self.contract.request(path, "post")
                self.assertMissingRejected(schema, valid, valid.keys())
                self.assertInvalid(schema, dict(valid, acknowledged=False))
                self.assertInvalid(schema, dict(valid, expectedVersion=0))
                self.assertInvalid(schema, dict(valid, termsHash=""))
        end = self.contract.request("/relations/{id}/end", "post")
        valid = {"expectedVersion": 2, "reason": "student_end"}
        self.assertMissingRejected(end, valid, valid.keys())

    def test_coach_plan_creation_update_and_publication_have_json_examples(self):
        create = self.contract.request("/coach/relations/{id}/plans", "post")
        update = self.contract.request("/coach/plans/{id}", "put")
        for factory in (training_content, diet_content):
            with self.subTest(kind=factory()["kind"]):
                valid = {"title": "教练交付测试", "goal": "beginner", "content": factory()}
                self.assertMissingRejected(create, valid, valid.keys())
                self.assertInvalid(create, dict(valid, relationId="999"))
                self.assertInvalid(create, dict(valid, reviewedByProfessional=True))
                edited = {"title": valid["title"], "expectedVersion": 2, "content": factory()}
                self.assertMissingRejected(update, edited, edited.keys())
        publish = self.contract.request("/coach/plans/{id}/publish", "post")
        valid = {"expectedVersion": 2, "acknowledged": True}
        self.assertMissingRejected(publish, valid, valid.keys())
        self.assertInvalid(publish, dict(valid, acknowledged=False))

    def test_comments_and_messages_do_not_accept_client_forged_actor_ids(self):
        cases = (("/relations/{id}/comments", {"text": "本周按感受调整负荷。", "planId": "101"}),
                 ("/appointments/{id}/messages", {"text": "下周三可以咨询。"}))
        for path, valid in cases:
            with self.subTest(path=path):
                schema = self.contract.request(path, "post")
                self.assertMissingRejected(schema, valid, ("text",))
                self.assertInvalid(schema, dict(valid, text=""))
                for field in ("authorId", "senderId", "relationId", "appointmentId"):
                    self.assertInvalid(schema, dict(valid, **{field: "999"}))


class PublicationExampleTests(ContractCase):
    def test_manual_publish_drafts_support_both_contents_and_versioned_updates(self):
        create = self.contract.request("/publish/drafts", "post")
        update = self.contract.request("/publish/drafts/{id}", "patch")
        for factory in (training_content, diet_content):
            content = factory()
            with self.subTest(kind=content["kind"]):
                valid = {"planType": content["kind"], "title": "手工发布测试", "goal": "beginner", "content": content}
                self.assertMissingRejected(create, valid, valid.keys())
                for field, forged in (("sourceTag", "official"), ("evidenceLevel", "professionally_reviewed"),
                                      ("authorType", "coach"), ("reviewLabel", "professionally_reviewed")):
                    self.assertInvalid(create, dict(valid, **{field: forged}))
                edit = {"title": valid["title"], "goal": valid["goal"], "content": content,
                        "expectedVersion": 2, "visibility": "public"}
                self.assertMissingRejected(update, edit, edit.keys())
                self.assertInvalid(update, dict(edit, expectedVersion=0))
        response = self.contract.response("/publish/drafts/{id}", "get")
        self.assertRequired(response, "id", "version", "estimatedFieldPaths", "visibility")
        self.contract.property(response, "contentHash")

    def test_draft_read_requires_complete_content_only_when_ready_or_submitted(self):
        schema = self.contract.response("/publish/drafts/{id}", "get")
        partial = {"id": "301", "version": 1, "planType": "diet", "estimatedFieldPaths": [],
                   "status": "editing", "warnings": [], "visibility": "private",
                   "fieldSuggestions": [], "missingFields": ["content.meals"]}
        self.assertValid(schema, partial)
        self.assertValid(schema, dict(partial, status="blocked"))
        for state in ("ready", "submitted"):
            with self.subTest(status=state):
                self.assertInvalid(schema, dict(partial, status=state))
                complete = dict(partial, status=state, title="完整测试草稿", goal="beginner",
                                content=diet_content(), contentHash="a" * 64)
                self.assertMissingRejected(schema, complete, ("content", "contentHash", "title", "goal", "visibility"))
        description = self.contract.deref(schema).get("description", "")
        self.assertIn("contentHash", description)
        self.assertIn("visibility", description)
        submit = self.contract.deref(self.contract.request("/publish/plans", "post"))
        self.assertIn("visibility", submit.get("description", ""))
        # Equality of stored hash/audience is deliberately a remaining runtime check.

    def test_ai_ready_content_is_typed_and_partial_suggestions_keep_unknowns(self):
        schema = self.contract.response("/publish/assist", "post")
        ready = {"draftId": "301", "draftVersion": 2, "aiRunId": "401", "state": "ready",
                 "contentHash": "a" * 64, "content": training_content(), "fieldSuggestions": [],
                 "missingFields": [], "warnings": []}
        self.assertValid(schema, ready)
        self.assertValid(schema, dict(ready, content=diet_content()))
        self.assertInvalid(schema, dict(ready, content={"kind": "diet", "meals": []}))
        self.assertInvalid(schema, dict(ready, sourceTag="official"))
        needs_input = {"draftId": "301", "draftVersion": 1, "aiRunId": "401", "state": "needs_input",
                       "fieldSuggestions": [{"path": "content.meals[0].foods[0].quantity",
                                             "provenance": "unknown", "needsConfirmation": True}],
                       "missingFields": ["content.meals[0].foods[0].quantity"], "warnings": []}
        self.assertValid(schema, needs_input)
        # No numeric amount, absent food, or empty structured plan is invented.
        invalid = copy.deepcopy(needs_input)
        invalid["fieldSuggestions"][0]["provenance"] = "guaranteed_fact"
        self.assertInvalid(schema, invalid)
        self.assertValid(schema, dict(needs_input, state="safety_block"))

    def test_publication_confirmation_requires_each_field_and_deduplicates_estimates(self):
        schema = self.contract.request("/publish/plans", "post")
        valid = {"draftId": "301", "expectedDraftVersion": 2, "visibility": "link",
                 "confirmation": {"contentHash": "a" * 64, "estimatedFieldPaths": [], "acknowledged": True}}
        self.assertValid(schema, valid)
        duplicate = copy.deepcopy(valid)
        duplicate["confirmation"]["estimatedFieldPaths"] = ["/content/weeks", "/content/weeks"]
        self.assertInvalid(schema, duplicate)
        empty = copy.deepcopy(valid)
        empty["confirmation"]["estimatedFieldPaths"] = [""]
        self.assertInvalid(schema, empty)
        self.assertInvalid(schema, dict(valid, expectedDraftVersion="2"))
        for field, value in (("content", training_content()), ("title", "另发正文"), ("draft", {}),
                             ("aiRunId", "401"), ("confirmed", True)):
            self.assertInvalid(schema, dict(valid, **{field: value}))


class EvidenceExampleTests(ContractCase):
    def test_diet_checkins_pin_the_observed_plan_version_and_day(self):
        schema = self.contract.request("/me/diet-plans/{id}/checkins", "post")
        valid = {"date": "2026-10-22", "expectedVersion": 0, "planVersion": 1, "dayIndex": 1,
                 "mealStatuses": [{"slotKey": "breakfast", "status": "unknown"}]}
        self.assertMissingRejected(schema, valid, ("planVersion", "dayIndex"))
        for changes in ({"planVersion": 0}, {"planVersion": "1"}, {"dayIndex": 0}, {"dayIndex": 32}):
            self.assertInvalid(schema, dict(valid, **changes))
        read = self.contract.response("/me/diet-plans/{id}/checkins", "post")
        self.assertRequired(read, "planVersion", "dayIndex")

    def test_allergy_consent_revocation_is_explicit_and_versioned(self):
        path = "/me/allergy-profile"
        self.assertVersioned(path, "delete")
        schema = self.contract.request(path, "delete")
        self.assertMissingRejected(schema, {"expectedVersion": 2}, ("expectedVersion",))
        operation = self.contract.operation(path, "delete")
        self.assertIn("204", operation["responses"])
        self.assertNotIn("content", self.contract.deref(operation["responses"]["204"]))
        self.assertRegex(operation.get("description", ""), r"撤回")

    def test_insufficient_data_is_valid_without_fabricated_observations(self):
        schema = self.contract.schema("AnomalyFlag", "DietAnomalyFlag")
        valid = {"id": "601", "targetType": "diet_checkin", "targetId": "701", "targetVersion": 1,
                 "level": "info", "assessment": "insufficient_data", "reasons": [], "evidence": [],
                 "ruleVersion": "diet-observation-v1", "status": "pending", "version": 1,
                 "createdAt": "2026-10-22T00:00:00Z"}
        self.assertMissingRejected(schema, valid, ("assessment", "evidence", "ruleVersion"))
        self.assertInvalid(schema, dict(valid, assessment="confirmed_malnutrition"))
        self.assertInvalid(schema, dict(valid, evidence=[{}]))
        valid["evidence"] = [{"kind": "self_report", "id": "701", "version": 1,
                              "provenance": "insufficient"}]
        self.assertValid(schema, valid)
        self.assertInvalid(schema, dict(valid, actualIntakeKcal=1800))

    def test_sensitive_profile_input_accepts_write_but_public_read_has_no_raw_notes(self):
        request = self.contract.request("/me/student-profile", "put")
        valid = {"goal": "beginner", "expectedVersion": 0, "injuryNotes": "合成测试文本",
                 "injuryConsent": True}
        self.assertValid(request, valid)
        self.assertValid(request, dict(valid, injuryNotes=None))
        self.assertInvalid(request, dict(valid, injuryNotes={"freeform": "wrong type"}))
        self.assertInvalid(request, dict(valid, privacyLevel="public"))
        for method in ("get", "put"):
            response = self.contract.response("/me/student-profile", method)
            # Structural property reachability is decisive; generated/readOnly
            # annotations alone are not used as JSON runtime enforcement.
            for _, node in self.contract.reachable(response):
                self.assertNotIn("injuryNotes", node.get("properties", {}))
