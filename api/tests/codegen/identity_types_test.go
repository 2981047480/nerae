// Generated model and schema probes; not HTTP, authentication, or database tests.
package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestGeneratedPendingLoginRoundTrip(t *testing.T) {
	pending := PhoneAuthApprovalRequired{
		ApprovalId: strings.Repeat("A", 22), ApprovalSecret: strings.Repeat("S", 43),
		Nonce: strings.Repeat("N", 43), ComparisonCode: "123456",
		ExpiresAt: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
		Status:    PhoneAuthApprovalRequiredStatus("approval_required"),
	}
	var union PhoneLoginOutcome
	if err := union.FromPhoneAuthApprovalRequired(pending); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(union)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["token"]; exists {
		t.Fatal("pending login must not contain a token")
	}
	got, err := union.AsPhoneAuthApprovalRequired()
	if err != nil || got.ApprovalId != pending.ApprovalId {
		t.Fatalf("pending login did not round-trip: %v", err)
	}
}

func TestGeneratedWechatVariantNamesRemainStable(t *testing.T) {
	identity := IdentityItem1{Kind: IdentityItem1KindWechatMini}
	claim := IdentityLinkClaim1{Kind: IdentityLinkClaim1KindWechatMini}
	start := IdentityLinkStart1{Kind: WechatMini}
	if string(identity.Kind) != "wechat_mini" || string(claim.Kind) != "wechat_mini" || string(start.Kind) != "wechat_mini" {
		t.Fatal("existing generated WeChat variants changed")
	}
}

func TestIdentitySchemasWithGoValidator(t *testing.T) {
	path := os.Getenv("NERAE_OPENAPI")
	if path == "" {
		t.Fatal("NERAE_OPENAPI must point to the contract")
	}
	doc, err := openapi3.NewLoader().LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, schema, value string
		valid               bool
	}{
		{"masked phone", "IdentityItem", `{"id":"AAAAAAAAAAAAAAAAAAAAAA","kind":"phone","phoneLast4":"8000"}`, true},
		{"full phone", "IdentityItem", `{"id":"AAAAAAAAAAAAAAAAAAAAAA","kind":"phone","phoneLast4":"+8613800138000"}`, false},
		{"masked email", "IdentityItem", `{"id":"AAAAAAAAAAAAAAAAAAAAAA","kind":"email","emailMasked":"p***@example.com"}`, true},
		{"full email", "IdentityItem", `{"id":"AAAAAAAAAAAAAAAAAAAAAA","kind":"email","emailMasked":"person@example.com"}`, false},
		{"email link", "IdentityLinkStart", `{"kind":"email","email":"person@example.com","expectedVersion":1}`, true},
		{"email login missing proof", "EmailLoginRequest", `{"challengeId":"AAAAAAAAAAAAAAAAAAAAAA","code":"123456"}`, false},
		{"wechat openid", "IdentityItem", `{"id":"AAAAAAAAAAAAAAAAAAAAAA","kind":"wechat_mini","openid":"private"}`, false},
		{"replace phone", "IdentityLinkStart", `{"kind":"phone","intent":"replace","phone":"+8613800138000","expectedVersion":1}`, true},
		{"missing intent", "IdentityLinkStart", `{"kind":"phone","phone":"+8613800138000","expectedVersion":1}`, false},
		{"recovery requires proof", "RecoveryCodeUseRequest", `{"approvalSecret":"SSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSS","recoveryCode":"AAAAAAAAAAAAAAAAAAAAAAAAAA"}`, false},
		{"status leaks code", "RecoveryCodeState", `{"state":"active","generation":1,"recoveryCode":"AAAAAAAAAAAAAAAAAAAAAAAAAA"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var v any
			if err := json.Unmarshal([]byte(tc.value), &v); err != nil {
				t.Fatal(err)
			}
			schema := doc.Components.Schemas[tc.schema]
			if schema == nil || schema.Value == nil {
				t.Fatalf("missing schema %s", tc.schema)
			}
			err := schema.Value.VisitJSON(v)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
