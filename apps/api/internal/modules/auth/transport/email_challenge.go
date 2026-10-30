package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
)

const emailChallengesPath = "/auth/email/challenges"

// EmailChallengeRequestIssuer is the narrow adapter implemented by the real
// issuer. HTTP tests may substitute it only for rejection-path assertions.
type EmailChallengeRequestIssuer interface {
	Issue(context.Context, *sql.DB, issuance.ChallengeRequestInput) (issuance.ChallengeRequestResult, error)
}

// TrustedSourceIPResolver must be supplied by the server's trusted network
// boundary. The handler never reads Forwarded or X-Forwarded-For itself.
type TrustedSourceIPResolver interface {
	ResolveTrustedSourceIP(*http.Request) (netip.Addr, error)
}

type TrustedSourceIPFunc func(*http.Request) (netip.Addr, error)

func (f TrustedSourceIPFunc) ResolveTrustedSourceIP(r *http.Request) (netip.Addr, error) {
	if f == nil || r == nil {
		return netip.Addr{}, errTrustedIP
	}
	return f(r)
}

var errTrustedIP = errors.New("trusted source unavailable")

type emailChallengeHandler struct {
	database  *sql.DB
	issuer    EmailChallengeRequestIssuer
	validator *middleware.SchemaValidator
	ip        TrustedSourceIPResolver
}

// NewEmailChallengeHandler builds an isolated handler. It does not mount a
// production route. Callers must inject an explicit trusted IP resolver.
func NewEmailChallengeHandler(db *sql.DB, issuer EmailChallengeRequestIssuer, validator *middleware.SchemaValidator, ip TrustedSourceIPResolver) (http.Handler, error) {
	if db == nil || nilAuthDependency(issuer) || validator == nil || nilAuthDependency(ip) {
		return nil, middleware.ErrContract
	}
	return &emailChallengeHandler{database: db, issuer: issuer, validator: validator, ip: ip}, nil
}

func nilAuthDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (h *emailChallengeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if w == nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r == nil {
		return
	}
	id, err := middleware.NewRequestID()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-Request-ID", id.String())
	if r.Method != http.MethodPost || r.URL == nil || r.URL.Path != emailChallengesPath || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		h.fail(w, id, middleware.BadRequest)
		return
	}
	idemKey, err := challengeIdempotencyKey(r.Header)
	if err != nil {
		h.fail(w, id, middleware.BadRequest)
		return
	}
	body, err := h.validator.ReadRequest(r, "EmailLoginChallengeRequest")
	if err != nil {
		h.fail(w, id, requestErrorKind(err))
		return
	}
	var request api.EmailLoginChallengeRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	if _, err := domain.NormalizeEmail(string(request.Email)); err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	publicKey := domain.DevicePublicJWK{Kty: string(request.DevicePublicKey.Kty), Crv: string(request.DevicePublicKey.Crv), X: request.DevicePublicKey.X, Y: request.DevicePublicKey.Y}
	if _, err := domain.DeviceKeyFingerprint(publicKey); err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	ip, err := h.ip.ResolveTrustedSourceIP(r)
	if err != nil {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	if !validSourceIP(ip) {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	result, err := h.issuer.Issue(r.Context(), h.db(), issuance.ChallengeRequestInput{
		Email: string(request.Email), DevicePublicKey: publicKey, DeviceLabel: request.DeviceLabel,
		IdempotencyKey: idemKey, TrustedSourceIP: ip,
	})
	if err != nil {
		h.fail(w, id, issuerErrorKind(err))
		return
	}
	wire := result.WireBody()
	if len(wire) == 0 || len(wire) > 2048 || !strictWireJSON(wire) {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	var response api.EmailLoginChallenge
	responseDecoder := json.NewDecoder(bytes.NewReader(wire))
	responseDecoder.DisallowUnknownFields()
	if err := responseDecoder.Decode(&response); err != nil || responseDecoder.Decode(new(any)) != io.EOF || !validChallengeResponse(response) {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	encoded, err := h.validator.EncodeResponse("EmailLoginChallenge", response)
	if err != nil {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(encoded)
}

func (h *emailChallengeHandler) db() *sql.DB {
	return h.database
}

// strictWireJSON rejects duplicate members and multiple top-level values even
// though issuer output is internal. The response is bounded before parsing.
func strictWireJSON(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if !scanWireValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func scanWireValue(decoder *json.Decoder, depth int) bool {
	if depth > 8 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{}, 4)
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return false
			}
			if _, exists := seen[key]; exists {
				return false
			}
			seen[key] = struct{}{}
			if !scanWireValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !scanWireValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

func (h *emailChallengeHandler) fail(w http.ResponseWriter, id middleware.RequestID, kind middleware.ErrorKind) {
	if err := middleware.WriteError(w, h.validator, id, kind); err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func challengeIdempotencyKey(header http.Header) (string, error) {
	values := header.Values("Idempotency-Key")
	if len(values) != 1 || !utf8.ValidString(values[0]) || len(values[0]) < 22 || len(values[0]) > 128 || strings.ContainsAny(values[0], " \t\r\n\x00") {
		return "", middleware.ErrInvalidRequest
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
	if err != nil || len(decoded) < 16 || base64.RawURLEncoding.EncodeToString(decoded) != values[0] {
		return "", middleware.ErrInvalidRequest
	}
	return values[0], nil
}

func validSourceIP(ip netip.Addr) bool {
	return ip.IsValid() && !ip.IsUnspecified() && !ip.IsMulticast() && ip.Zone() == ""
}

func validChallengeResponse(value api.EmailLoginChallenge) bool {
	if len(value.ChallengeId) < 22 || len(value.ChallengeId) > 128 || value.ExpiresAt.IsZero() || !value.ExpiresAt.After(time.Now().UTC()) {
		return false
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(string(value.Nonce))
	if err != nil || len(nonce) != 32 || base64.RawURLEncoding.EncodeToString(nonce) != string(value.Nonce) {
		return false
	}
	if value.RetryAfter != nil && value.RetryAfter.IsZero() {
		return false
	}
	return true
}

func requestErrorKind(err error) middleware.ErrorKind {
	if errors.Is(err, middleware.ErrContract) {
		return middleware.Unavailable
	}
	return middleware.ValidationError
}

func issuerErrorKind(err error) middleware.ErrorKind {
	switch {
	case errors.Is(err, issuance.ErrChallengeRequestLimited):
		return middleware.RateLimited
	case errors.Is(err, issuance.ErrChallengeRequestConflict):
		return middleware.Conflict
	default:
		return middleware.Unavailable
	}
}
