package idempotency

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

const maxBodyBytes = 4 << 20
const maxJSONNodes = 100000
const maxJSONDepth = 64

// normalizeRequest is deliberately independent of the HTTP decoder. Callers
// must additionally validate the operation's schema and resource authorization.
func normalizeRequest(input Request) (Request, error) {
	if input.Method != "POST" && input.Method != "PUT" && input.Method != "PATCH" && input.Method != "DELETE" {
		return Request{}, ErrInvalid
	}
	if !validPath(input.Path) || !utf8.ValidString(input.Key) || utf8.RuneCountInString(input.Key) < 8 ||
		utf8.RuneCountInString(input.Key) > 128 || strings.ContainsAny(input.Key, "\x00\r\n") ||
		len(input.RawQuery) > 4096 || len(input.Body) > maxBodyBytes {
		return Request{}, ErrInvalid
	}
	values, err := url.ParseQuery(input.RawQuery)
	if err != nil {
		return Request{}, ErrInvalid
	}
	var body []byte
	if len(input.Body) > 0 {
		body, err = canonicalJSON(input.Body)
		if err != nil {
			return Request{}, ErrInvalid
		}
	}
	return Request{Method: input.Method, Path: input.Path, RawQuery: values.Encode(), Key: input.Key, Body: body}, nil
}

func validPath(value string) bool {
	if len(value) == 0 || len(value) > 256 || value[0] != '/' || path.Clean(value) != value {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') &&
			r != '/' && r != '-' && r != '_' && r != '.' && r != '~' {
			return false
		}
	}
	return true
}

// canonicalJSON sorts object keys via encoding/json, preserves array order,
// number spelling and null versus omission, and rejects duplicate keys. Empty
// HTTP bodies remain distinct from JSON null. Schema validation is separate.
func canonicalJSON(body []byte) ([]byte, error) {
	if len(body) == 0 || len(body) > maxBodyBytes || !utf8.Valid(body) {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	remaining := maxJSONNodes
	value, err := readValue(decoder, 0, &remaining)
	if err != nil {
		return nil, ErrInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	output, err := json.Marshal(value)
	if err != nil || len(output) > maxBodyBytes {
		return nil, ErrInvalid
	}
	return output, nil
}

func readValue(decoder *json.Decoder, depth int, remaining *int) (any, error) {
	if *remaining <= 0 {
		return nil, ErrInvalid
	}
	*remaining--
	token, err := decoder.Token()
	if err != nil {
		return nil, ErrInvalid
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	if depth >= maxJSONDepth {
		return nil, ErrInvalid
	}
	switch delimiter {
	case '{':
		result := make(map[string]any)
		for decoder.More() {
			if *remaining <= 0 {
				return nil, ErrInvalid
			}
			*remaining--
			keyToken, err := decoder.Token()
			key, valid := keyToken.(string)
			if err != nil || !valid {
				return nil, ErrInvalid
			}
			if _, exists := result[key]; exists {
				return nil, ErrInvalid
			}
			value, err := readValue(decoder, depth+1, remaining)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrInvalid
		}
		return result, nil
	case '[':
		result := make([]any, 0)
		for decoder.More() {
			value, err := readValue(decoder, depth+1, remaining)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrInvalid
		}
		return result, nil
	default:
		return nil, ErrInvalid
	}
}
