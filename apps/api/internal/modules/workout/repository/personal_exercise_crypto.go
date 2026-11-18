package repository

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"

	"nerae/api/internal/modules/workout/domain"
)

var errPersonalExerciseProtection = errors.New("personal exercise protection unavailable")

type personalExerciseProtector struct {
	aead     cipher.AEAD
	hashKey  [32]byte
	keyID    string
	epochTag [32]byte
}

func (personalExerciseProtector) String() string   { return "personalExerciseProtector{redacted}" }
func (personalExerciseProtector) GoString() string { return "personalExerciseProtector{redacted}" }
func (personalExerciseProtector) MarshalJSON() ([]byte, error) {
	return nil, errPersonalExerciseProtection
}
func (personalExerciseProtector) LogValue() slog.Value {
	return slog.StringValue("personalExerciseProtector{redacted}")
}

type personalExercisePayload struct {
	Name         string
	MuscleGroups []string
	Equipment    []string
}

func newPersonalExerciseProtector(root []byte, keyID string) (*personalExerciseProtector, error) {
	if _, err := newRecordingV2Protector(root, keyID); err != nil {
		return nil, errPersonalExerciseProtection
	}
	derive := func(label string) []byte {
		mac := hmac.New(sha256.New, root)
		_, _ = mac.Write([]byte(label + "\x00" + keyID))
		return mac.Sum(nil)
	}
	key := derive("nerae/personal-exercise/aead/v1")
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		return nil, errPersonalExerciseProtection
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errPersonalExerciseProtection
	}
	hash := derive("nerae/personal-exercise/name/v1")
	result := &personalExerciseProtector{aead: aead, keyID: keyID}
	copy(result.hashKey[:], hash)
	clear(hash)
	epoch := derive("nerae/personal-exercise/key-epoch/v1")
	copy(result.epochTag[:], epoch)
	clear(epoch)
	return result, nil
}

func personalExerciseAAD(subject uint64, id string, version uint64, keyID string) ([]byte, error) {
	rawID, err := hex.DecodeString(id)
	if err != nil || len(rawID) != 16 || hex.EncodeToString(rawID) != id || subject == 0 || version == 0 {
		return nil, errPersonalExerciseProtection
	}
	const prefix = "nerae/personal-exercise/snapshot/v1\x00"
	result := make([]byte, len(prefix)+8+16+8+len(keyID))
	copy(result, prefix)
	binary.BigEndian.PutUint64(result[len(prefix):], subject)
	copy(result[len(prefix)+8:], rawID)
	binary.BigEndian.PutUint64(result[len(prefix)+24:], version)
	copy(result[len(prefix)+32:], keyID)
	return result, nil
}

func (p *personalExerciseProtector) nameHash(subject uint64, name string) ([]byte, error) {
	key, err := domain.PersonalExerciseNameKey(name)
	if p == nil || subject == 0 || err != nil {
		return nil, errPersonalExerciseProtection
	}
	mac := hmac.New(sha256.New, p.hashKey[:])
	_, _ = mac.Write([]byte("nerae/personal-exercise/owner-name/v1\x00"))
	var owner [8]byte
	binary.BigEndian.PutUint64(owner[:], subject)
	_, _ = mac.Write(owner[:])
	_, _ = mac.Write([]byte(key))
	return mac.Sum(nil), nil
}

func (p *personalExerciseProtector) seal(subject uint64, value domain.PersonalExercise) ([]byte, error) {
	if p == nil || p.aead == nil || value.Version == 0 {
		return nil, errPersonalExerciseProtection
	}
	normalized, err := domain.NormalizePersonalExercise(value)
	if err != nil {
		return nil, errPersonalExerciseProtection
	}
	aad, err := personalExerciseAAD(subject, normalized.ID, normalized.Version, p.keyID)
	if err != nil {
		return nil, errPersonalExerciseProtection
	}
	plain, err := json.Marshal(personalExercisePayload{Name: normalized.Name, MuscleGroups: normalized.MuscleGroups, Equipment: normalized.Equipment})
	if err != nil {
		return nil, errPersonalExerciseProtection
	}
	defer clear(plain)
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errPersonalExerciseProtection
	}
	return p.aead.Seal(nonce, nonce, plain, aad), nil
}

func (p *personalExerciseProtector) open(subject uint64, id string, version uint64, sealed []byte) (domain.PersonalExercise, error) {
	if p == nil || p.aead == nil || len(sealed) < p.aead.NonceSize()+p.aead.Overhead()+1 || len(sealed) > 16384 {
		return domain.PersonalExercise{}, errPersonalExerciseProtection
	}
	aad, err := personalExerciseAAD(subject, id, version, p.keyID)
	if err != nil {
		return domain.PersonalExercise{}, errPersonalExerciseProtection
	}
	plain, err := p.aead.Open(nil, sealed[:p.aead.NonceSize()], sealed[p.aead.NonceSize():], aad)
	if err != nil {
		return domain.PersonalExercise{}, errPersonalExerciseProtection
	}
	defer clear(plain)
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	var payload personalExercisePayload
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF {
		return domain.PersonalExercise{}, errPersonalExerciseProtection
	}
	canonical, err := json.Marshal(payload)
	defer clear(canonical)
	if err != nil || !bytes.Equal(canonical, plain) || payload.MuscleGroups == nil || payload.Equipment == nil {
		return domain.PersonalExercise{}, errPersonalExerciseProtection
	}
	value, err := domain.NormalizePersonalExercise(domain.PersonalExercise{ID: id, Version: version,
		Name: payload.Name, MuscleGroups: payload.MuscleGroups, Equipment: payload.Equipment})
	if err != nil || value.Name != payload.Name || !equalStrings(value.MuscleGroups, payload.MuscleGroups) ||
		!equalStrings(value.Equipment, payload.Equipment) {
		return domain.PersonalExercise{}, errPersonalExerciseProtection
	}
	return value, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
