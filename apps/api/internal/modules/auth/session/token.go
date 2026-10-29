// Package session provides internal session primitives, not HTTP authorization.
package session

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidToken = errors.New("invalid user token")

const userTokenIssuer = "nerae-users"

type TokenPolicy struct {
	MaxLifetime time.Duration
	ClockSkew   time.Duration
}

type TokenCodec struct {
	key    []byte
	policy TokenPolicy
	now    func() time.Time
}

// NewTokenCodec requires an externally generated high-entropy key. It does not
// load secrets, rotate keys, issue database sessions, or authorize requests.
func NewTokenCodec(key []byte, policy TokenPolicy) (*TokenCodec, error) {
	if len(key) != 32 || policy.MaxLifetime < time.Second || policy.MaxLifetime > 30*24*time.Hour ||
		policy.MaxLifetime%time.Second != 0 || policy.ClockSkew < 0 || policy.ClockSkew > 5*time.Minute || policy.ClockSkew%time.Second != 0 {
		return nil, ErrInvalidToken
	}
	return &TokenCodec{key: append([]byte(nil), key...), policy: policy, now: time.Now}, nil
}

func (TokenCodec) String() string               { return "TokenCodec{redacted}" }
func (TokenCodec) GoString() string             { return "TokenCodec{redacted}" }
func (TokenCodec) MarshalJSON() ([]byte, error) { return nil, ErrInvalidToken }
func (TokenCodec) LogValue() slog.Value         { return slog.StringValue("TokenCodec{redacted}") }

type AccessToken struct{ value string }

func (AccessToken) String() string               { return "AccessToken{redacted}" }
func (AccessToken) GoString() string             { return "AccessToken{redacted}" }
func (AccessToken) MarshalJSON() ([]byte, error) { return nil, ErrInvalidToken }
func (AccessToken) LogValue() slog.Value         { return slog.StringValue("AccessToken{redacted}") }

// ForResponse deliberately exposes the credential. The caller must commit the
// authenticated session transaction before returning it and must not log it.
func (t AccessToken) ForResponse() string { return t.value }

// TokenClaims are verified cryptographic claims only, not an authenticated
// principal. Every request still needs current database session, user,
// identity, device, version, and resource checks in the consuming transaction.
type TokenClaims struct {
	SubjectID    string
	SessionID    string
	TokenVersion uint64
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

func (TokenClaims) String() string               { return "TokenClaims{redacted}" }
func (TokenClaims) GoString() string             { return "TokenClaims{redacted}" }
func (TokenClaims) MarshalJSON() ([]byte, error) { return nil, ErrInvalidToken }
func (TokenClaims) LogValue() slog.Value         { return slog.StringValue("TokenClaims{redacted}") }

type tokenPayload struct {
	Issuer       string `json:"iss"`
	Audience     string `json:"aud"`
	SubjectID    string `json:"sub"`
	SessionID    string `json:"jti"`
	TokenVersion uint64 `json:"tv"`
	IssuedAt     int64  `json:"iat"`
	ExpiresAt    int64  `json:"exp"`
}

// Issue signs a server-selected subject, session identifier and token version.
// Those values must come from a completed proof and the same live DB transaction.
func (c *TokenCodec) Issue(subjectID, sessionID string, tokenVersion uint64) (AccessToken, error) {
	if c == nil || len(c.key) != 32 || c.now == nil || !validSubject(subjectID) || !validSessionID(sessionID) || tokenVersion == 0 {
		return AccessToken{}, ErrInvalidToken
	}
	now := c.now().UTC()
	if now.IsZero() || now.Unix() < 1 {
		return AccessToken{}, ErrInvalidToken
	}
	iat := now.Unix()
	exp := now.Truncate(time.Second).Add(c.policy.MaxLifetime).Unix()
	if exp <= iat {
		return AccessToken{}, ErrInvalidToken
	}
	payload, err := json.Marshal(tokenPayload{userTokenIssuer, userTokenIssuer, subjectID, sessionID, tokenVersion, iat, exp})
	if err != nil {
		return AccessToken{}, ErrInvalidToken
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + encode(payload)
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte(unsigned))
	return AccessToken{value: unsigned + "." + encode(mac.Sum(nil))}, nil
}

func (c *TokenCodec) Verify(compact string) (TokenClaims, error) {
	if c == nil || len(c.key) != 32 || c.now == nil || len(compact) < 32 || len(compact) > 8192 {
		return TokenClaims{}, ErrInvalidToken
	}
	parts := strings.Split(compact, ".")
	if len(parts) != 3 {
		return TokenClaims{}, ErrInvalidToken
	}
	decode := base64.RawURLEncoding.Strict().DecodeString
	header, errHeader := decode(parts[0])
	payload, errPayload := decode(parts[1])
	signature, errSignature := decode(parts[2])
	if errHeader != nil || errPayload != nil || errSignature != nil || len(signature) != sha256.Size {
		return TokenClaims{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return TokenClaims{}, ErrInvalidToken
	}
	if !exactObject(header, "alg", "typ") || !bytes.Equal(header, []byte(`{"alg":"HS256","typ":"JWT"}`)) {
		return TokenClaims{}, ErrInvalidToken
	}
	if !exactObject(payload, "iss", "aud", "sub", "jti", "tv", "iat", "exp") {
		return TokenClaims{}, ErrInvalidToken
	}
	var raw tokenPayload
	if err := json.Unmarshal(payload, &raw); err != nil || raw.Issuer != userTokenIssuer || raw.Audience != userTokenIssuer ||
		!validSubject(raw.SubjectID) || !validSessionID(raw.SessionID) || raw.TokenVersion == 0 || raw.IssuedAt < 1 || raw.ExpiresAt <= raw.IssuedAt ||
		raw.ExpiresAt-raw.IssuedAt > int64(c.policy.MaxLifetime/time.Second) {
		return TokenClaims{}, ErrInvalidToken
	}
	now := c.now().UTC()
	if now.IsZero() || now.Unix() < 1 || raw.IssuedAt > now.Add(c.policy.ClockSkew).Unix() || !time.Unix(raw.ExpiresAt, 0).After(now) {
		return TokenClaims{}, ErrInvalidToken
	}
	return TokenClaims{SubjectID: raw.SubjectID, SessionID: raw.SessionID, TokenVersion: raw.TokenVersion,
		IssuedAt: time.Unix(raw.IssuedAt, 0).UTC(), ExpiresAt: time.Unix(raw.ExpiresAt, 0).UTC()}, nil
}

func validSubject(value string) bool {
	if len(value) == 0 || len(value) > 20 || value[0] == '0' {
		return false
	}
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id != 0 && strconv.FormatUint(id, 10) == value
}

func validSessionID(value string) bool {
	if len(value) < 22 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// exactObject enforces exact property names and detects duplicate JSON fields;
// the byte limit on the compact token bounds parsing before this function runs.
func exactObject(data []byte, names ...string) bool {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		seen, exists := allowed[name]
		if !exists || seen {
			return false
		}
		allowed[name] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return false
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return false
	}
	for _, seen := range allowed {
		if !seen {
			return false
		}
	}
	return true
}
