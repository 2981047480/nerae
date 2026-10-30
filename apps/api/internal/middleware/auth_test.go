package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBearerHeaderIsStrictAndNeverExposesInputInErrors(t *testing.T) {
	token := strings.Repeat("a", 32)
	for _, value := range []string{"Bearer " + token, "bearer  " + token} {
		header := make(http.Header)
		header.Set("Authorization", value)
		got, err := BearerToken(header)
		if err != nil || got != token {
			t.Fatal("valid bearer rejected")
		}
	}
	for _, value := range []string{
		"", "Basic " + token, "Bearer", "Bearer ", " Bearer " + token,
		"Bearer\t" + token, "Bearer " + token + " ", "Bearer " + token + ",other",
		"Bearer " + token + "\r\nsecret-token", "Bearer " + strings.Repeat("x", 8193),
	} {
		header := make(http.Header)
		header.Set("Authorization", value)
		got, err := BearerToken(header)
		if got != "" || !errors.Is(err, ErrInvalidBearer) || strings.Contains(err.Error(), "secret-token") {
			t.Fatal("invalid bearer accepted or leaked")
		}
	}
	header := make(http.Header)
	header.Add("Authorization", "Bearer "+token)
	header.Add("Authorization", "Bearer "+token)
	if _, err := BearerToken(header); !errors.Is(err, ErrInvalidBearer) {
		t.Fatal("multiple credentials accepted")
	}
}

func TestAuthenticationCannotBeFabricatedFromHeadersWithoutLiveDependencies(t *testing.T) {
	r := httptest.NewRequest("GET", "/me/sessions/42", nil)
	r.Header.Set("X-User-Id", "101")
	r.Header.Set("X-Role", "coach")
	if principal, err := AuthenticateRequest(r, nil, nil); principal != nil || !errors.Is(err, ErrAuthUnavailable) {
		t.Fatal("headers fabricated a principal")
	}
}
