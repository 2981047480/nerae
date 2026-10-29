// Package contractnullable preserves JSON's omitted/null/value distinction
// while preventing an unset required field from being encoded as a zero value.
// Its map representation follows github.com/oapi-codegen/nullable (Apache-2.0).
package contractnullable

import (
	"bytes"
	"encoding/json"
	"errors"
)

var (
	ErrUnspecified = errors.New("nullable value is unspecified")
	ErrNull        = errors.New("nullable value is null")
	ErrInvalid     = errors.New("nullable value has conflicting states")
)

// Nullable is empty when unspecified; false represents null and true a value.
// Use omitempty only for fields that may be omitted. A required field without
// omitempty fails marshaling until SetNull or Set has been called.
type Nullable[T any] map[bool]T

func NewNullNullable[T any]() Nullable[T] {
	var value Nullable[T]
	value.SetNull()
	return value
}

func NewNullableWithValue[T any](item T) Nullable[T] {
	var value Nullable[T]
	value.Set(item)
	return value
}

func (value Nullable[T]) IsSpecified() bool { return len(value) != 0 }

func (value Nullable[T]) IsNull() bool {
	_, ok := value[false]
	return ok
}

func (value Nullable[T]) Get() (T, error) {
	var empty T
	if len(value) > 1 {
		return empty, ErrInvalid
	}
	if !value.IsSpecified() {
		return empty, ErrUnspecified
	}
	if value.IsNull() {
		return empty, ErrNull
	}
	return value[true], nil
}

func (value Nullable[T]) MustGet() T {
	item, err := value.Get()
	if err != nil {
		panic(err)
	}
	return item
}

func (value *Nullable[T]) Set(item T) { *value = Nullable[T]{true: item} }

func (value *Nullable[T]) SetNull() {
	var empty T
	*value = Nullable[T]{false: empty}
}

func (value *Nullable[T]) SetUnspecified() { *value = nil }

func (value Nullable[T]) MarshalJSON() ([]byte, error) {
	if len(value) == 0 {
		return nil, ErrUnspecified
	}
	if len(value) > 1 {
		return nil, ErrInvalid
	}
	if value.IsNull() {
		return []byte("null"), nil
	}
	return json.Marshal(value[true])
}

func (value *Nullable[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.SetNull()
		return nil
	}
	var item T
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	value.Set(item)
	return nil
}
