// Package domain contains identity rules. It does not issue sessions or grant access.
package domain

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidDeviceProof = errors.New("invalid device proof")

const maxProofClockSkew = 5 * time.Minute

// DevicePublicJWK must be obtained from the server's own challenge record.
type DevicePublicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// ProofExpectation and the public key must come from a trusted, unexpired
// server-side transaction, never from a request's unverified identity claim.
type ProofExpectation struct {
	Purpose    string
	ResourceID string
	Nonce      string
}

type ProofPolicy struct {
	MaxLifetime time.Duration
	ClockSkew   time.Duration
}

// VerifiedDeviceProof is only a cryptographic result. The caller must
// atomically consume JTI through ReplayUntil, check SMS and device trust, and
// issue a session in a separate transaction. JTI and proof contents must not
// be logged.
type VerifiedDeviceProof struct {
	JTI         string
	ExpiresAt   time.Time
	ReplayUntil time.Time
}

// VerifyDeviceProof validates the contract's compact ES256 JWS. It never
// consults a key supplied in the JWS header or makes an authorization decision.
func VerifyDeviceProof(compact string, key DevicePublicJWK, expected ProofExpectation, now time.Time, policy ProofPolicy) (VerifiedDeviceProof, error) {
	invalid := func() (VerifiedDeviceProof, error) { return VerifiedDeviceProof{}, ErrInvalidDeviceProof }
	if len(compact) < 32 || len(compact) > 4096 || now.IsZero() || policy.MaxLifetime <= 0 ||
		policy.MaxLifetime > 24*time.Hour || policy.ClockSkew < 0 || policy.ClockSkew > maxProofClockSkew ||
		!validPurpose(expected.Purpose) || expected.ResourceID == "" || expected.Nonce == "" {
		return invalid()
	}
	public, ok := parsePublicKey(key)
	if !ok {
		return invalid()
	}
	parts := strings.Split(compact, ".")
	if len(parts) != 3 {
		return invalid()
	}
	protected, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return invalid()
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return invalid()
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		return invalid()
	}
	header, ok := uniqueObject(protected)
	if !ok || header["alg"] != "ES256" || header["typ"] != "JWT" {
		return invalid()
	}
	// This verifier supports only ordinary base64url JWS payloads and no
	// critical extensions. It must never silently ignore critical semantics.
	if _, exists := header["crit"]; exists {
		return invalid()
	}
	if b64, exists := header["b64"]; exists && b64 != true {
		return invalid()
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(public, digest[:], r, s) {
		return invalid()
	}
	claims, ok := uniqueObject(payload)
	if !ok || claims["aud"] != "nerae-auth-device" || claims["purpose"] != expected.Purpose {
		return invalid()
	}
	resource, resourceOK := claims["resourceId"].(string)
	nonce, nonceOK := claims["nonce"].(string)
	jti, jtiOK := claims["jti"].(string)
	if !resourceOK || !nonceOK || !jtiOK || jti == "" || len(jti) > 256 ||
		!hmac.Equal([]byte(resource), []byte(expected.ResourceID)) || !hmac.Equal([]byte(nonce), []byte(expected.Nonce)) {
		return invalid()
	}
	iat, iatOK := numericDate(claims["iat"])
	exp, expOK := numericDate(claims["exp"])
	if !iatOK || !expOK {
		return invalid()
	}
	current := new(big.Rat).Add(new(big.Rat).SetInt64(now.Unix()), big.NewRat(int64(now.Nanosecond()), int64(time.Second)))
	lifetime := big.NewRat(int64(policy.MaxLifetime), int64(time.Second))
	skew := big.NewRat(int64(policy.ClockSkew), int64(time.Second))
	if exp.Cmp(iat) <= 0 || new(big.Rat).Sub(exp, iat).Cmp(lifetime) > 0 ||
		iat.Cmp(new(big.Rat).Add(current, skew)) > 0 || exp.Cmp(new(big.Rat).Sub(current, skew)) <= 0 {
		return invalid()
	}
	expiresAt, ok := ceilingTime(exp)
	if !ok {
		return invalid()
	}
	// Keep the replay marker for the largest allowed skew, even if an operator
	// later increases the configured skew while this proof is still alive.
	return VerifiedDeviceProof{JTI: jti, ExpiresAt: expiresAt, ReplayUntil: expiresAt.Add(maxProofClockSkew)}, nil
}

func validPurpose(purpose string) bool {
	return purpose == "phone_login" || purpose == "approval_exchange" || purpose == "recovery_code"
}

func parsePublicKey(key DevicePublicJWK) (*ecdsa.PublicKey, bool) {
	if key.Kty != "EC" || key.Crv != "P-256" || len(key.X) != 43 || len(key.Y) != 43 {
		return nil, false
	}
	x, errX := base64.RawURLEncoding.Strict().DecodeString(key.X)
	y, errY := base64.RawURLEncoding.Strict().DecodeString(key.Y)
	if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
		return nil, false
	}
	curve := elliptic.P256()
	pointX, pointY := new(big.Int).SetBytes(x), new(big.Int).SetBytes(y)
	if !curve.IsOnCurve(pointX, pointY) {
		return nil, false
	}
	return &ecdsa.PublicKey{Curve: curve, X: pointX, Y: pointY}, true
}

func numericDate(value any) (*big.Rat, bool) {
	number, ok := value.(json.Number)
	if !ok || len(number.String()) > 32 {
		return nil, false
	}
	raw := number.String()
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(raw[i+1:])
		if err != nil || exponent < -32 || exponent > 32 {
			return nil, false
		}
	}
	seconds, ok := new(big.Rat).SetString(raw)
	if !ok || new(big.Rat).Abs(seconds).Cmp(new(big.Rat).SetInt64(100000000000)) > 0 {
		return nil, false
	}
	return seconds, true
}

// Round upward so a fractional expiry never shortens the replay-retention
// deadline when a database records timestamps at nanosecond precision.
func ceilingTime(seconds *big.Rat) (time.Time, bool) {
	whole, remainder := new(big.Int), new(big.Int)
	whole.DivMod(seconds.Num(), seconds.Denom(), remainder)
	nanos, nanoRemainder := new(big.Int), new(big.Int)
	nanos.DivMod(new(big.Int).Mul(remainder, big.NewInt(int64(time.Second))), seconds.Denom(), nanoRemainder)
	if nanoRemainder.Sign() > 0 {
		nanos.Add(nanos, big.NewInt(1))
	}
	if nanos.Cmp(big.NewInt(int64(time.Second))) == 0 {
		whole.Add(whole, big.NewInt(1))
		nanos.SetInt64(0)
	}
	if !whole.IsInt64() {
		return time.Time{}, false
	}
	return time.Unix(whole.Int64(), nanos.Int64()).UTC(), true
}

// Decode token-by-token so duplicate JSON members cannot be interpreted
// differently by a signer, verifier, proxy or later transaction code.
func uniqueObject(data []byte) (map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readJSONValue(decoder, 0)
	if err != nil {
		return nil, false
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	object, ok := value.(map[string]any)
	return object, ok
}

func readJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 16 {
		return nil, ErrInvalidDeviceProof
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			name, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := name.(string)
			if !ok {
				return nil, ErrInvalidDeviceProof
			}
			if _, duplicate := object[key]; duplicate {
				return nil, ErrInvalidDeviceProof
			}
			object[key], err = readJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrInvalidDeviceProof
		}
		return object, nil
	case '[':
		var array []any
		for decoder.More() {
			value, err := readJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrInvalidDeviceProof
		}
		return array, nil
	default:
		return nil, ErrInvalidDeviceProof
	}
}
