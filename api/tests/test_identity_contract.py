"""Identity wire-schema regressions; these do not implement or test authentication."""

from test_static_contract import ContractCase


class IdentityContractTests(ContractCase):
    challenge_id = 'C' * 22
    link_id = 'L' * 22
    approval_id = 'A' * 22
    secret = 'S' * 43
    public_key = {'kty': 'EC', 'crv': 'P-256', 'x': 'X' * 43, 'y': 'Y' * 43}
    # Shape only: deliberately not presented as a cryptographically valid signature.
    proof = 'A' * 20 + '.' + 'B' * 30 + '.' + 'C' * 86

    def test_wechat_login_remains_its_own_operation_and_rejects_spoofed_identity(self):
        path = '/auth/wechat-login'
        self.assertEqual(self.contract.operation(path, 'post')['operationId'], 'postAuthWechatLogin')
        request = self.contract.request(path, 'post')
        self.assertValid(request, {'code': 'wx-test-code'})
        self.assertValid(request, {'code': 'wx-test-code', 'role': 'student'})
        for bad in ({}, {'code': ''}, {'code': 'wx-test-code', 'userId': '101'},
                    {'code': 'wx-test-code', 'openid': 'forged'}, {'code': 'wx-test-code', 'role': 'admin'}):
            with self.subTest(value=bad):
                self.assertInvalid(request, bad)

    def test_phone_challenge_requires_normalized_phone_and_public_key_only(self):
        request = self.contract.request('/auth/phone/challenges', 'post')
        valid = {'phone': '+8613800138000', 'devicePublicKey': self.public_key}
        self.assertMissingRejected(request, valid, ('phone', 'devicePublicKey'))
        for bad in (dict(valid, phone='13800138000'), dict(valid, phone='+0'), dict(valid, phone='+12'),
                    dict(valid, userId='101'), dict(valid, devicePublicKey=dict(self.public_key, d='private')),
                    dict(valid, devicePublicKey=dict(self.public_key, crv='P-384')),
                    dict(valid, devicePublicKey=dict(self.public_key, x='x'))):
            with self.subTest(value=bad):
                self.assertInvalid(request, bad)

    def test_beta_email_login_binds_challenge_and_device_proof(self):
        path = '/auth/email/challenges'
        request = self.contract.request(path, 'post')
        valid = {'email': 'person@example.com', 'devicePublicKey': self.public_key}
        self.assertMissingRejected(request, valid, ('email', 'devicePublicKey'))
        for bad in (dict(valid, email='not-an-address'), dict(valid, email='a@@example.com'),
                    dict(valid, email='person@example.com\r\nBcc:other@example.com'),
                    dict(valid, userId='101'), dict(valid, devicePublicKey=dict(self.public_key, d='private'))):
            self.assertInvalid(request, bad)
        response = self.contract.response(path, 'post', '202')
        challenge = {'challengeId': self.challenge_id, 'nonce': self.secret, 'expiresAt': '2026-10-29T12:00:00Z'}
        self.assertValid(response, challenge)
        self.assertInvalid(response, dict(challenge, code='123456'))
        self.assertInvalid(response, dict(challenge, userId='101'))
        login = self.contract.request('/auth/email-login', 'post')
        submitted = {'challengeId': self.challenge_id, 'code': '123456', 'deviceProof': self.proof}
        self.assertMissingRejected(login, submitted, submitted.keys())
        self.assertInvalid(login, dict(submitted, code='123'))
        self.assertInvalid(login, dict(submitted, email='person@example.com'))

    def test_beta_email_link_and_masked_identity(self):
        start = self.contract.request('/me/identity-links', 'post')
        valid = {'kind': 'email', 'email': 'person@example.com', 'expectedVersion': 1}
        self.assertMissingRejected(start, valid, valid.keys())
        self.assertInvalid(start, dict(valid, userId='101'))
        claim = self.contract.request('/auth/identity-link-claims', 'post')
        proof = {'kind': 'email', 'linkId': self.link_id, 'linkSecret': self.secret, 'code': '123456'}
        self.assertMissingRejected(claim, proof, proof.keys())
        self.assertInvalid(claim, dict(proof, code='123'))
        result = self.contract.response('/me/identities', 'get')
        self.assertValid(result, {'version': 1, 'items': [{'id': self.link_id, 'kind': 'email', 'emailMasked': 'p***@example.com'}]})
        self.assertInvalid(result, {'version': 1, 'items': [{'id': self.link_id, 'kind': 'email', 'emailMasked': 'person@example.com'}]})
        self.assertInvalid(result, {'version': 1, 'items': [{'id': self.link_id, 'kind': 'email', 'emailMasked': 'p***@example.com', 'email': 'person@example.com'}]})

    def test_phone_login_requires_sms_code_and_device_proof(self):
        request = self.contract.request('/auth/phone-login', 'post')
        valid = {'challengeId': self.challenge_id, 'code': '123456', 'deviceProof': self.proof}
        self.assertMissingRejected(request, valid, valid.keys())
        for bad in (dict(valid, code='12345'), dict(valid, code='abcdef'), dict(valid, deviceProof='unsigned'),
                    dict(valid, challengeId='short'), dict(valid, userId='101'), dict(valid, openid='forged')):
            with self.subTest(value=bad):
                self.assertInvalid(request, bad)

    def test_unapproved_login_cannot_include_token_or_user(self):
        pending = {'status': 'approval_required', 'approvalId': self.approval_id,
                   'approvalSecret': self.secret, 'nonce': self.secret,
                   'comparisonCode': '123456', 'expiresAt': '2026-10-28T12:00:00Z'}
        success = {'status': 'authenticated', 'token': 'test-session-token', 'expiresIn': 3600,
                   'user': {'id': '101', 'nickname': '测试用户', 'roles': ['student']}}
        for path in ('/auth/phone-login', '/auth/email-login'):
            with self.subTest(path=path):
                result = self.contract.response(path, 'post')
                self.assertMissingRejected(result, pending, pending.keys())
                self.assertValid(result, success)
                self.assertInvalid(result, dict(pending, token='must-not-be-returned'))
                self.assertInvalid(result, dict(pending, user=success['user']))
                self.assertInvalid(result, {'status': 'authenticated', 'token': 'token'})

    def test_approval_decision_requires_explicit_comparison_for_approval(self):
        request = self.contract.request('/me/login-approvals/{id}/decision', 'post')
        self.assertValid(request, {'decision': 'approve', 'comparisonCode': '123456', 'confirmed': True})
        self.assertValid(request, {'decision': 'deny'})
        for bad in ({}, {'decision': 'approve'}, {'decision': 'approve', 'confirmed': True},
                    {'decision': 'approve', 'comparisonCode': '123456', 'confirmed': False},
                    {'decision': 'deny', 'confirmed': True}, {'decision': 'approve', 'comparisonCode': 'abcd', 'confirmed': True}):
            with self.subTest(value=bad):
                self.assertInvalid(request, bad)

    def test_approval_exchange_requires_secret_and_same_device_proof(self):
        request = self.contract.request('/auth/login-approvals/{id}/exchange', 'post')
        valid = {'approvalSecret': self.secret, 'deviceProof': self.proof}
        self.assertMissingRejected(request, valid, valid.keys())
        self.assertInvalid(request, dict(valid, userId='101'))
        result = self.contract.response('/auth/login-approvals/{id}/exchange', 'post')
        for state in ('pending', 'denied', 'expired'):
            self.assertValid(result, {'status': state})
            self.assertInvalid(result, {'status': state, 'token': 'leaked'})

    def test_link_requires_current_subject_other_proof_and_confirmation(self):
        start = self.contract.request('/me/identity-links', 'post')
        self.assertValid(start, {'kind': 'phone', 'intent': 'add', 'phone': '+8613800138000', 'expectedVersion': 1})
        self.assertValid(start, {'kind': 'phone', 'intent': 'replace', 'phone': '+8613800138000', 'expectedVersion': 1})
        self.assertValid(start, {'kind': 'wechat_mini', 'expectedVersion': 1})
        for bad in ({}, {'kind': 'phone', 'expectedVersion': 1}, {'kind': 'phone', 'phone': '+8613800138000', 'expectedVersion': 1},
                    {'kind': 'wechat_mini', 'phone': '+8613800138000', 'expectedVersion': 1},
                    {'kind': 'wechat_mini', 'expectedVersion': 0},
                    {'kind': 'wechat_mini', 'expectedVersion': 1, 'userId': '101'}):
            self.assertInvalid(start, bad)
        claim = self.contract.request('/auth/identity-link-claims', 'post')
        phone = {'kind': 'phone', 'linkId': self.link_id, 'linkSecret': self.secret, 'code': '123456'}
        wx = {'kind': 'wechat_mini', 'linkId': self.link_id, 'linkSecret': self.secret, 'code': 'wx-test-code'}
        self.assertMissingRejected(claim, phone, phone.keys())
        self.assertValid(claim, wx)
        self.assertInvalid(claim, dict(wx, openid='forged'))
        self.assertInvalid(claim, dict(phone, code='123'))
        confirm = self.contract.request('/me/identity-links/{id}/confirm', 'post')
        valid = {'expectedVersion': 1, 'comparisonCode': '123456', 'confirmed': True}
        self.assertMissingRejected(confirm, valid, valid.keys())
        self.assertInvalid(confirm, dict(valid, confirmed=False))
        self.assertInvalid(confirm, dict(valid, expectedVersion=0))

    def test_link_status_keeps_confirmation_distinct_from_success(self):
        result = self.contract.response('/me/identity-links/{id}', 'get')
        self.assertValid(result, {'status': 'waiting_confirmation', 'comparisonCode': '123456'})
        for status in ('waiting_proof', 'linked', 'cancelled', 'expired', 'conflict'):
            self.assertValid(result, {'status': status})
            self.assertInvalid(result, {'status': status, 'comparisonCode': '123456'})
        self.assertInvalid(result, {'status': 'waiting_confirmation'})

    def test_unlink_and_device_revoke_require_explicit_confirmation(self):
        unlink = self.contract.request('/me/identities/{identityId}', 'delete')
        self.assertValid(unlink, {'expectedVersion': 1, 'confirmed': True})
        for bad in ({}, {'expectedVersion': 1}, {'expectedVersion': 1, 'confirmed': False},
                    {'expectedVersion': 0, 'confirmed': True}):
            self.assertInvalid(unlink, bad)
        revoke = self.contract.request('/me/trusted-devices/{id}/revoke', 'post')
        self.assertValid(revoke, {'confirmed': True})
        self.assertInvalid(revoke, {'confirmed': False})
        self.assertInvalid(revoke, {})

    def test_identity_list_exposes_only_phone_suffix_not_full_identity(self):
        response = self.contract.response('/me/identities', 'get')
        valid = {'version': 1, 'items': [{'id': self.link_id, 'kind': 'phone', 'phoneLast4': '8000'},
                                         {'id': self.approval_id, 'kind': 'wechat_mini'}]}
        self.assertValid(response, valid)
        for bad in ({'version': 1, 'items': [{'id': self.link_id, 'kind': 'phone', 'phoneLast4': '+8613800138000'}]},
                    {'version': 1, 'items': [{'id': self.link_id, 'kind': 'phone', 'phoneLast4': '8000', 'displayLabel': '+8613800138000'}]},
                    {'version': 1, 'items': [{'id': self.link_id, 'kind': 'wechat_mini', 'openid': 'private'}]}):
            self.assertInvalid(response, bad)

    def test_revoke_all_requires_explicit_device_choice(self):
        request = self.contract.request('/me/sessions/revoke-all', 'post')
        self.assertValid(request, {'confirmed': True, 'revokeTrustedDevices': True})
        self.assertValid(request, {'confirmed': True, 'revokeTrustedDevices': False})
        for bad in ({}, {'confirmed': True}, {'confirmed': False, 'revokeTrustedDevices': True}):
            self.assertInvalid(request, bad)

    def test_sensitive_auth_success_responses_disallow_http_caching(self):
        for path, method, status in (('/auth/email/challenges', 'post', '202'),
                                     ('/auth/email-login', 'post', '200'),
                                     ('/auth/phone/challenges', 'post', '202'),
                                     ('/auth/phone-login', 'post', '200'),
                                     ('/auth/login-approvals/{id}/exchange', 'post', '200'),
                                     ('/auth/wechat-login', 'post', '200')):
            response = self.contract.deref(self.contract.operation(path, method)['responses'][status])
            header = response['headers']['Cache-Control']
            self.assertTrue(header['required'])
            self.assertEqual(header['schema']['enum'], ['no-store'])

    def test_public_and_authenticated_boundaries_and_retry_keys_are_explicit(self):
        public = [('/auth/email/challenges', 'post'), ('/auth/email-login', 'post'),
                  ('/auth/phone/challenges', 'post'), ('/auth/phone-login', 'post'),
                  ('/auth/login-approvals/{id}/exchange', 'post'),
                  ('/auth/identity-link-claims', 'post'), ('/auth/identity-link-claims/{id}/status', 'post')]
        for path, method in public:
            with self.subTest(path=path):
                self.assertEqual(self.contract.operation(path, method).get('security'), [])
                keys = [p for p in self.contract.parameters(path, method) if p['name'] == 'Idempotency-Key']
                self.assertEqual(len(keys), 1)
                self.assertGreaterEqual(keys[0]['schema']['minLength'], 22)
                self.assertTrue(keys[0]['required'])
        for path, method in (('/me/identities', 'get'), ('/me/identity-links', 'post'),
                             ('/me/login-approvals/{id}/decision', 'post'),
                             ('/me/trusted-devices/{id}/revoke', 'post')):
            operation = self.contract.operation(path, method)
            self.assertNotEqual(operation.get('security'), [])
            self.assertIn('401', operation['responses'])
        self.assertTrue(self.contract.doc['security'])

    def test_auth_errors_do_not_allow_http_caching(self):
        for path, method in (('/auth/wechat-login', 'post'), ('/auth/phone-login', 'post'),
                             ('/me/identity-links/{id}/confirm', 'post'),
                             ('/me/sessions/revoke-all', 'post')):
            with self.subTest(path=path):
                response = self.contract.deref(self.contract.operation(path, method)['responses']['409'])
                self.assertTrue(response['headers']['Cache-Control']['required'])
                self.assertEqual(response['headers']['Cache-Control']['schema']['enum'], ['no-store'])

    def test_sensitive_writes_require_strong_idempotency_keys(self):
        for path, method in (('/me/identity-links', 'post'),
                             ('/me/identity-links/{id}/confirm', 'post'),
                             ('/me/identity-links/{id}/cancel', 'post'),
                             ('/me/login-approvals/{id}/decision', 'post'),
                             ('/me/identities/{identityId}', 'delete'),
                             ('/me/trusted-devices/{id}/revoke', 'post'),
                             ('/me/sessions/revoke-all', 'post')):
            with self.subTest(path=path):
                keys = [p for p in self.contract.parameters(path, method) if p['name'] == 'Idempotency-Key']
                self.assertEqual(len(keys), 1)
                self.assertTrue(keys[0]['required'])
                self.assertGreaterEqual(keys[0]['schema']['minLength'], 22)

    def test_recovery_code_setup_is_versioned_and_explicitly_confirmed(self):
        issue = self.contract.request('/me/recovery-code/issues', 'post')
        self.assertValid(issue, {'expectedGeneration': 0, 'confirmed': True})
        for bad in ({}, {'expectedGeneration': -1, 'confirmed': True},
                    {'expectedGeneration': 0}, {'expectedGeneration': 0, 'confirmed': False}):
            self.assertInvalid(issue, bad)
        confirm = self.contract.request('/me/recovery-code/issues/{id}/confirm', 'post')
        self.assertValid(confirm, {'expectedGeneration': 1, 'confirmed': True})
        self.assertInvalid(confirm, {'expectedGeneration': 1, 'confirmed': False})
        result = self.contract.response('/me/recovery-code', 'get')
        self.assertValid(result, {'state': 'active', 'generation': 1})
        self.assertInvalid(result, {'state': 'active', 'generation': 1, 'recoveryCode': 'A' * 26})

    def test_recovery_requires_code_device_proof_and_original_approval_secret(self):
        request = self.contract.request('/auth/login-approvals/{id}/recover-with-code', 'post')
        valid = {'approvalSecret': self.secret, 'recoveryCode': 'A' * 26, 'deviceProof': self.proof}
        self.assertMissingRejected(request, valid, valid.keys())
        for bad in (dict(valid, recoveryCode='123456'), dict(valid, recoveryCode='A' * 25),
                    dict(valid, deviceProof='unsigned'), dict(valid, userId='101')):
            self.assertInvalid(request, bad)
        response = self.contract.response('/auth/login-approvals/{id}/recover-with-code', 'post')
        self.assertInvalid(response, {'status': 'pending', 'token': 'leak'})

    def test_recovery_response_headers_and_replay_key(self):
        for path, method, status in (('/me/recovery-code/issues', 'post', '201'),
                                     ('/auth/login-approvals/{id}/recover-with-code', 'post', '200')):
            response = self.contract.deref(self.contract.operation(path, method)['responses'][status])
            self.assertEqual(response['headers']['Cache-Control']['schema']['enum'], ['no-store'])
            keys = [p for p in self.contract.parameters(path, method) if p['name'] == 'Idempotency-Key']
            self.assertEqual(len(keys), 1)
            self.assertGreaterEqual(keys[0]['schema']['minLength'], 22)

    def test_issued_recovery_code_and_active_confirmation_shapes(self):
        issued = self.contract.response('/me/recovery-code/issues', 'post', '201')
        valid = {'id': self.link_id, 'recoveryCode': 'A' * 26, 'expiresAt': '2026-10-28T12:00:00Z'}
        self.assertMissingRejected(issued, valid, valid.keys())
        for bad in (dict(valid, recoveryCode='123456'), dict(valid, recoveryCode='A' * 25),
                    dict(valid, recoveryCode='a' * 26), dict(valid, userId='101')):
            self.assertInvalid(issued, bad)
        result = self.contract.response('/me/recovery-code/issues/{id}/confirm', 'post')
        self.assertValid(result, {'state': 'active', 'generation': 1})
        self.assertInvalid(result, {'state': 'not_configured', 'generation': 1})
        self.assertInvalid(result, {'state': 'active', 'generation': 0})
        keys = [p for p in self.contract.parameters('/me/recovery-code/issues/{id}/confirm', 'post')
                if p['name'] == 'Idempotency-Key']
        self.assertEqual(len(keys), 1)
        self.assertGreaterEqual(keys[0]['schema']['minLength'], 22)
