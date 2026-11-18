package repository

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/workout/domain"
)

var (
	ErrPersonalExerciseNotFound  = errors.New("personal exercise not found")
	ErrPersonalExerciseNameTaken = errors.New("personal exercise name already used")
)

const (
	personalExerciseListSQL = `SELECT e.exercise_id, e.version, e.key_id, e.name_digest, e.snapshot_enc,
		v.key_id, v.name_digest, v.snapshot_enc FROM workout_personal_exercise e
		LEFT JOIN workout_personal_exercise_snapshot v ON v.user_id = e.user_id AND v.exercise_id = e.exercise_id AND v.version = e.version
		WHERE e.user_id = ? AND e.exercise_id > ? ORDER BY e.exercise_id LIMIT ? FOR SHARE`
	personalExerciseReadEpochSQL = `SELECT key_id, key_tag FROM workout_personal_exercise_key_epoch WHERE slot = 1 FOR SHARE`
	personalExerciseLockEpochSQL = `SELECT key_id, key_tag FROM workout_personal_exercise_key_epoch WHERE slot = 1 FOR UPDATE`
	personalExerciseAnySQL       = `SELECT exercise_id FROM workout_personal_exercise LIMIT 1 FOR UPDATE`
	personalExerciseSetEpochSQL  = `UPDATE workout_personal_exercise_key_epoch SET key_id = ?, key_tag = ? WHERE slot = 1`
	personalExerciseFindSQL      = `SELECT version, key_id, name_digest, snapshot_enc FROM workout_personal_exercise
		WHERE user_id = ? AND exercise_id = ? FOR UPDATE`
	personalExerciseFindSnapshotSQL = `SELECT key_id, name_digest, snapshot_enc FROM workout_personal_exercise_snapshot
		WHERE user_id = ? AND exercise_id = ? AND version = ? FOR SHARE`
	personalExerciseInsertSnapshotSQL = `INSERT INTO workout_personal_exercise_snapshot
		(user_id, exercise_id, version, key_id, name_digest, snapshot_enc) VALUES (?, ?, ?, ?, ?, ?)`
	personalExerciseAnySnapshotSQL = `SELECT exercise_id FROM workout_personal_exercise_snapshot LIMIT 1 FOR UPDATE`
	personalExerciseFindNameSQL    = `SELECT exercise_id FROM workout_personal_exercise
		WHERE user_id = ? AND name_digest = ? FOR UPDATE`
	personalExerciseInsertSQL = `INSERT INTO workout_personal_exercise
		(user_id, exercise_id, version, key_id, name_digest, snapshot_enc, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	personalExerciseUpdateSQL = `UPDATE workout_personal_exercise SET version = ?, key_id = ?, name_digest = ?, snapshot_enc = ?, updated_at = ?
		WHERE user_id = ? AND exercise_id = ? AND version = ?`
)

// PersonalExerciseStore is only a persistence primitive. Its caller must use
// the authenticated owner, enforce age and manage the transaction; it is not
// an HTTP endpoint or permission to use an action in a training record.
type PersonalExerciseStore struct {
	protector *personalExerciseProtector
	now       func() time.Time
}

func (PersonalExerciseStore) String() string               { return "PersonalExerciseStore{redacted}" }
func (PersonalExerciseStore) GoString() string             { return "PersonalExerciseStore{redacted}" }
func (PersonalExerciseStore) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (PersonalExerciseStore) LogValue() slog.Value {
	return slog.StringValue("PersonalExerciseStore{redacted}")
}

func NewPersonalExerciseStore(root []byte, keyID string) (*PersonalExerciseStore, error) {
	p, err := newPersonalExerciseProtector(root, keyID)
	if err != nil {
		return nil, ErrUnavailable
	}
	return &PersonalExerciseStore{protector: p, now: time.Now}, nil
}

// InitializeKey may initialize only an empty library; a missing, mismatched,
// partially initialized or reset key epoch fails closed.
func (s *PersonalExerciseStore) InitializeKey(ctx context.Context, tx *sql.Tx) error {
	if s == nil || s.protector == nil || ctx == nil || tx == nil {
		return ErrUnavailable
	}
	var keyID, tag []byte
	if err := tx.QueryRowContext(ctx, personalExerciseLockEpochSQL).Scan(&keyID, &tag); err != nil {
		return ErrUnavailable
	}
	if keyID == nil && tag == nil {
		var unused []byte
		if err := tx.QueryRowContext(ctx, personalExerciseAnySQL).Scan(&unused); !errors.Is(err, sql.ErrNoRows) {
			return ErrUnavailable
		}
		if err := tx.QueryRowContext(ctx, personalExerciseAnySnapshotSQL).Scan(&unused); !errors.Is(err, sql.ErrNoRows) {
			return ErrUnavailable
		}
		result, err := tx.ExecContext(ctx, personalExerciseSetEpochSQL, []byte(s.protector.keyID), s.protector.epochTag[:])
		if err != nil {
			return ErrUnavailable
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return ErrUnavailable
		}
		return nil
	}
	if string(keyID) != s.protector.keyID || !hmac.Equal(tag, s.protector.epochTag[:]) {
		return ErrUnavailable
	}
	return nil
}

func personalExerciseID(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != value || value == "00000000000000000000000000000000" {
		return nil, ErrInvalid
	}
	return decoded, nil
}

func (s *PersonalExerciseStore) lockOwnerAndEpoch(ctx context.Context, tx dbtx.ReadTransaction, subject uint64) error {
	if s == nil || s.protector == nil || s.now == nil || ctx == nil || tx == nil || subject == 0 {
		return ErrInvalid
	}
	if err := lockV2Subject(ctx, tx, subject); err != nil {
		return err
	}
	var id, tag []byte
	if err := tx.QueryRowContext(ctx, personalExerciseReadEpochSQL).Scan(&id, &tag); err != nil ||
		string(id) != s.protector.keyID || !hmac.Equal(tag, s.protector.epochTag[:]) {
		return ErrUnavailable
	}
	return nil
}

func (s *PersonalExerciseStore) readLocked(ctx context.Context, tx dbtx.ReadTransaction, subject uint64, id string, rawID []byte) (domain.PersonalExercise, bool, error) {
	var version uint64
	var keyID, digest, sealed []byte
	err := tx.QueryRowContext(ctx, personalExerciseFindSQL, subject, rawID).Scan(&version, &keyID, &digest, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PersonalExercise{}, false, nil
	}
	if err != nil || version == 0 || version > 9007199254740991 || string(keyID) != s.protector.keyID {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	value, err := s.protector.open(subject, id, version, sealed)
	if err != nil {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	computed, err := s.protector.nameHash(subject, value.Name)
	if err != nil || !hmac.Equal(computed, digest) {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	_, snapshotKey, snapshotDigest, snapshotCipher, err := s.snapshotLocked(ctx, tx, subject, id, rawID, version)
	if err != nil || !hmac.Equal(snapshotKey, keyID) || !hmac.Equal(snapshotDigest, digest) || !hmac.Equal(snapshotCipher, sealed) {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	return value, true, nil
}

func (s *PersonalExerciseStore) snapshotLocked(ctx context.Context, tx dbtx.ReadTransaction, subject uint64,
	id string, rawID []byte, version uint64) (domain.PersonalExercise, []byte, []byte, []byte, error) {
	var keyID, digest, sealed []byte
	err := tx.QueryRowContext(ctx, personalExerciseFindSnapshotSQL, subject, rawID, version).Scan(&keyID, &digest, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PersonalExercise{}, nil, nil, nil, ErrPersonalExerciseNotFound
	}
	if err != nil || string(keyID) != s.protector.keyID {
		return domain.PersonalExercise{}, nil, nil, nil, ErrUnavailable
	}
	value, err := s.protector.open(subject, id, version, sealed)
	if err != nil {
		return domain.PersonalExercise{}, nil, nil, nil, ErrUnavailable
	}
	computed, err := s.protector.nameHash(subject, value.Name)
	if err != nil || !hmac.Equal(computed, digest) {
		return domain.PersonalExercise{}, nil, nil, nil, ErrUnavailable
	}
	return value, keyID, digest, sealed, nil
}

// PersonalExercisePage contains only one authorized owner's entries.
type PersonalExercisePage struct {
	Items      []domain.PersonalExercise
	NextCursor string
}

func (PersonalExercisePage) String() string               { return "PersonalExercisePage{redacted}" }
func (PersonalExercisePage) GoString() string             { return "PersonalExercisePage{redacted}" }
func (PersonalExercisePage) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (PersonalExercisePage) LogValue() slog.Value {
	return slog.StringValue("PersonalExercisePage{redacted}")
}

// ListTx scans encrypted rows in bounded pages, then searches decrypted names
// in memory. A nonempty cursor means the caller must request another page,
// even if this page contains no matching items.
func (s *PersonalExerciseStore) ListTx(ctx context.Context, tx dbtx.ReadTransaction, subject uint64,
	query, cursor string, limit int) (PersonalExercisePage, error) {
	if limit < 1 || limit > 50 {
		return PersonalExercisePage{}, ErrInvalid
	}
	query = strings.TrimSpace(query)
	key := ""
	if query != "" {
		var err error
		key, err = domain.PersonalExerciseNameKey(query)
		if err != nil {
			return PersonalExercisePage{}, ErrInvalid
		}
	}
	after := make([]byte, 16)
	if cursor != "" {
		var err error
		after, err = personalExerciseID(cursor)
		if err != nil {
			return PersonalExercisePage{}, ErrInvalid
		}
	}
	if err := s.lockOwnerAndEpoch(ctx, tx, subject); err != nil {
		return PersonalExercisePage{}, err
	}
	const maximumScan = 200
	rows, err := tx.QueryContext(ctx, personalExerciseListSQL, subject, after, maximumScan+1)
	if err != nil {
		return PersonalExercisePage{}, ErrUnavailable
	}
	defer rows.Close()
	result := PersonalExercisePage{Items: make([]domain.PersonalExercise, 0, limit)}
	var last string
	seen := 0
	more := false
	for rows.Next() {
		if seen >= maximumScan || len(result.Items) >= limit {
			more = true
			break
		}
		var rawID, keyID, digest, sealed, snapshotKey, snapshotDigest, snapshotCipher []byte
		var version uint64
		if rows.Scan(&rawID, &version, &keyID, &digest, &sealed, &snapshotKey, &snapshotDigest, &snapshotCipher) != nil || len(rawID) != 16 ||
			version == 0 || version > 9007199254740991 || string(keyID) != s.protector.keyID ||
			!hmac.Equal(keyID, snapshotKey) || !hmac.Equal(digest, snapshotDigest) || !hmac.Equal(sealed, snapshotCipher) {
			return PersonalExercisePage{}, ErrUnavailable
		}
		id := hex.EncodeToString(rawID)
		if _, err := personalExerciseID(id); err != nil {
			return PersonalExercisePage{}, ErrUnavailable
		}
		value, err := s.protector.open(subject, id, version, sealed)
		if err != nil {
			return PersonalExercisePage{}, ErrUnavailable
		}
		nameKey, err := domain.PersonalExerciseNameKey(value.Name)
		if err != nil {
			return PersonalExercisePage{}, ErrUnavailable
		}
		computed, err := s.protector.nameHash(subject, value.Name)
		if err != nil || !hmac.Equal(computed, digest) {
			return PersonalExercisePage{}, ErrUnavailable
		}
		seen++
		last = id
		if key == "" || strings.Contains(nameKey, key) {
			result.Items = append(result.Items, value)
		}
	}
	if err := rows.Err(); err != nil {
		return PersonalExercisePage{}, ErrUnavailable
	}
	if more {
		result.NextCursor = last
	}
	return result, nil
}

func samePersonalExercise(a, b domain.PersonalExercise) bool {
	return a.ID == b.ID && a.Name == b.Name && equalStrings(a.MuscleGroups, b.MuscleGroups) && equalStrings(a.Equipment, b.Equipment)
}

// ReadTx returns only this owner's action; unknown and foreign IDs have the
// same result. Reads and writes use the same owner-first lock order.
func (s *PersonalExerciseStore) ReadTx(ctx context.Context, tx dbtx.ReadTransaction, subject uint64, id string) (domain.PersonalExercise, error) {
	rawID, err := personalExerciseID(id)
	if err != nil {
		return domain.PersonalExercise{}, err
	}
	if err := s.lockOwnerAndEpoch(ctx, tx, subject); err != nil {
		return domain.PersonalExercise{}, err
	}
	value, found, err := s.readLocked(ctx, tx, subject, id, rawID)
	if err != nil {
		return domain.PersonalExercise{}, err
	}
	if !found {
		return domain.PersonalExercise{}, ErrPersonalExerciseNotFound
	}
	return value, nil
}

// ReadVersionTx returns an immutable name/tag snapshot of an owned action.
func (s *PersonalExerciseStore) ReadVersionTx(ctx context.Context, tx dbtx.ReadTransaction, subject uint64,
	id string, version uint64) (domain.PersonalExercise, error) {
	rawID, err := personalExerciseID(id)
	if err != nil || version == 0 || version > 9007199254740991 {
		return domain.PersonalExercise{}, ErrInvalid
	}
	if err := s.lockOwnerAndEpoch(ctx, tx, subject); err != nil {
		return domain.PersonalExercise{}, err
	}
	current, found, err := s.readLocked(ctx, tx, subject, id, rawID)
	if err != nil {
		return domain.PersonalExercise{}, err
	}
	if !found || version > current.Version {
		return domain.PersonalExercise{}, ErrPersonalExerciseNotFound
	}
	value, _, _, _, err := s.snapshotLocked(ctx, tx, subject, id, rawID, version)
	if err != nil {
		return domain.PersonalExercise{}, err
	}
	return value, nil
}

// PutTx creates at expectedVersion 0 or updates an existing version. A replay
// of the immediately preceding identical mutation is harmless. This checks
// only personal-name uniqueness: standard-catalog collisions must be enforced
// before this storage primitive is exposed for application use.
func (s *PersonalExerciseStore) PutTx(ctx context.Context, tx dbtx.Transaction, subject uint64,
	input domain.PersonalExercise, expectedVersion uint64) (domain.PersonalExercise, bool, error) {
	if input.Version != 0 || expectedVersion > 9007199254740991 {
		return domain.PersonalExercise{}, false, ErrInvalid
	}
	value, err := domain.NormalizePersonalExercise(input)
	if err != nil {
		return domain.PersonalExercise{}, false, ErrInvalid
	}
	rawID, err := personalExerciseID(value.ID)
	if err != nil {
		return domain.PersonalExercise{}, false, err
	}
	if err := s.lockOwnerAndEpoch(ctx, tx, subject); err != nil {
		return domain.PersonalExercise{}, false, err
	}
	current, found, err := s.readLocked(ctx, tx, subject, value.ID, rawID)
	if err != nil {
		return domain.PersonalExercise{}, false, err
	}
	if !found && expectedVersion != 0 {
		return domain.PersonalExercise{}, false, ErrPersonalExerciseNotFound
	}
	if found {
		if expectedVersion == 0 {
			if current.Version == 1 && samePersonalExercise(current, value) {
				return current, false, nil
			}
			return domain.PersonalExercise{}, false, ErrVersionConflict
		}
		if current.Version != expectedVersion {
			if expectedVersion < 9007199254740991 && current.Version == expectedVersion+1 && samePersonalExercise(current, value) {
				return current, false, nil
			}
			return domain.PersonalExercise{}, false, ErrVersionConflict
		}
		if samePersonalExercise(current, value) {
			return current, false, nil
		}
		if current.Version == 9007199254740991 {
			return domain.PersonalExercise{}, false, ErrUnavailable
		}
	}
	digest, err := s.protector.nameHash(subject, value.Name)
	if err != nil {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	var duplicateID []byte
	err = tx.QueryRowContext(ctx, personalExerciseFindNameSQL, subject, digest).Scan(&duplicateID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	if err == nil && !hmac.Equal(duplicateID, rawID) {
		return domain.PersonalExercise{}, false, ErrPersonalExerciseNameTaken
	}
	value.Version = 1
	if found {
		value.Version = current.Version + 1
	}
	sealed, err := s.protector.seal(subject, value)
	if err != nil {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	stamp, err := v2Timestamp(s.now())
	if err != nil {
		return domain.PersonalExercise{}, false, err
	}
	var result sql.Result
	if found {
		result, err = tx.ExecContext(ctx, personalExerciseUpdateSQL, value.Version, []byte(s.protector.keyID), digest,
			sealed, stamp, subject, rawID, current.Version)
	} else {
		result, err = tx.ExecContext(ctx, personalExerciseInsertSQL, subject, rawID, value.Version, []byte(s.protector.keyID),
			digest, sealed, stamp, stamp)
	}
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return domain.PersonalExercise{}, false, ErrPersonalExerciseNameTaken
		}
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	result, err = tx.ExecContext(ctx, personalExerciseInsertSnapshotSQL, subject, rawID, value.Version,
		[]byte(s.protector.keyID), digest, sealed)
	if err != nil {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return domain.PersonalExercise{}, false, ErrUnavailable
	}
	return value, true, nil
}
