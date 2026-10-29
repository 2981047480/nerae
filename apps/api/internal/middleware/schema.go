// Package middleware contains transport boundaries. Schema validation never
// authenticates an identity or authorizes access to a resource.
package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
	gen "nerae/api/internal/gen"
)

var (
	ErrInvalidRequest  = errors.New("invalid request body")
	ErrInvalidResponse = errors.New("invalid response body")
	ErrContract        = errors.New("contract validation unavailable")
)

// SchemaValidator validates JSON bodies against the embedded generated contract.
// Callers must choose the schema and size limit for each operation, enforce
// authentication and authorization, and map the generic errors at the HTTP edge.
type SchemaValidator struct {
	schemas  openapi3.Schemas
	limit    int64
	maxNodes int
}

const maxBodyLimit int64 = 4 << 20
const maxJSONDepth = 64
const maxNodeLimit = 100_000

func NewSchemaValidator(limit int64, maxNodes int) (*SchemaValidator, error) {
	if limit < 1 || limit > maxBodyLimit || maxNodes < 1 || maxNodes > maxNodeLimit {
		return nil, ErrContract
	}
	doc, err := gen.GetSwagger()
	if err != nil || doc == nil || doc.Components == nil || doc.Components.Schemas == nil {
		return nil, ErrContract
	}
	return &SchemaValidator{schemas: doc.Components.Schemas, limit: limit, maxNodes: maxNodes}, nil
}

// ReadRequest consumes r.Body, reads at most limit+1 bytes and returns unchanged
// bytes for domain validation and decoding. Errors contain no caller-supplied data.
// Exact decimal precision must be checked on those bytes before float conversion.
func (v *SchemaValidator) ReadRequest(r *http.Request, schemaName string) ([]byte, error) {
	if v == nil || r == nil || r.Body == nil || r.ContentLength > v.limit {
		return nil, ErrInvalidRequest
	}
	values := r.Header.Values("Content-Type")
	if len(values) != 1 {
		return nil, ErrInvalidRequest
	}
	media, params, err := mime.ParseMediaType(values[0])
	if err != nil || media != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return nil, ErrInvalidRequest
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, v.limit+1))
	if err != nil || int64(len(body)) > v.limit {
		return nil, ErrInvalidRequest
	}
	if err := v.validate(schemaName, body, true); err != nil {
		return nil, err
	}
	return body, nil
}

// EncodeResponse serializes before validation, so missing required values and
// nullable encoding errors cannot be hidden by generated Go zero values. Marshal
// allocates before the byte limit is checked; callers must bound source results.
func (v *SchemaValidator) EncodeResponse(schemaName string, value any) ([]byte, error) {
	if v == nil {
		return nil, ErrContract
	}
	body, err := json.Marshal(value)
	if err != nil || int64(len(body)) > v.limit {
		return nil, ErrInvalidResponse
	}
	if err := v.validate(schemaName, body, false); err != nil {
		return nil, err
	}
	return body, nil
}

func (v *SchemaValidator) validate(name string, body []byte, request bool) error {
	if v == nil || v.schemas == nil {
		return ErrContract
	}
	schema := v.schemas[name]
	if schema == nil || schema.Value == nil {
		return ErrContract
	}
	invalid := ErrInvalidResponse
	if request {
		invalid = ErrInvalidRequest
	}
	value, err := strictJSON(body, v.maxNodes)
	if err != nil {
		return invalid
	}
	option := openapi3.VisitAsResponse()
	if request {
		option = openapi3.VisitAsRequest()
	}
	if err := schema.Value.VisitJSON(value, option, openapi3.EnableFormatValidation(), openapi3.FailFast()); err != nil {
		return invalid
	}
	return nil
}

// strictJSON rejects duplicate object members at every level, invalid UTF-8,
// multiple top-level values and excessive nesting. It preserves numeric text
// for downstream decoders; business decimal/range checks still belong to domain code.
func strictJSON(body []byte, maxNodes int) (any, error) {
	if !utf8.Valid(body) {
		return nil, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	remaining := maxNodes
	value, err := readJSONValue(decoder, 0, &remaining)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidRequest
	}
	return value, nil
}

func readJSONValue(decoder *json.Decoder, depth int, remaining *int) (any, error) {
	if *remaining <= 0 {
		return nil, ErrInvalidRequest
	}
	*remaining--
	token, err := decoder.Token()
	if err != nil {
		return nil, ErrInvalidRequest
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	if depth >= maxJSONDepth {
		return nil, ErrInvalidRequest
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			if *remaining <= 0 {
				return nil, ErrInvalidRequest
			}
			*remaining--
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return nil, ErrInvalidRequest
			}
			if _, exists := object[key]; exists {
				return nil, ErrInvalidRequest
			}
			value, err := readJSONValue(decoder, depth+1, remaining)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrInvalidRequest
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := readJSONValue(decoder, depth+1, remaining)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrInvalidRequest
		}
		return array, nil
	default:
		return nil, ErrInvalidRequest
	}
}
