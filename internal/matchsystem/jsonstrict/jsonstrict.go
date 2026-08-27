// Package jsonstrict provides the common structural checks required by the
// Contract and Evaluation JSON boundaries: duplicate keys, trailing values,
// nesting depth, and string byte limits.
package jsonstrict

import (
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// Options controls the optional resource limits applied by ValidateWithOptions.
// Zero means unlimited.
type Options struct {
	MaxBytes        int
	MaxDepth        int
	MaxObjectFields int
	MaxArrayItems   int
	MaxValues       int
	MaxStringBytes  int
}

// Error is a structural JSON boundary error. Callers translate it into their
// package-specific JSON error while retaining the precise path and code.
type Error struct {
	Path string
	Code string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("JSON at %s [%s]: %v", e.Path, e.Code, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func structuralError(path, code, format string, args ...any) error {
	return &Error{Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

// ValidateWithOptions performs domain-neutral structural validation. Callers
// can translate Error to their own error type without changing the path or
// code.
func ValidateWithOptions(data []byte, options Options) error {
	if options.MaxBytes > 0 && len(data) > options.MaxBytes {
		return structuralError("$", "JSON_SIZE_LIMIT", "JSON contains %d bytes; maximum is %d", len(data), options.MaxBytes)
	}
	if !utf8.Valid(data) {
		return structuralError("$", "INVALID_UTF8", "JSON must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytesReader(data))
	decoder.UseNumber()
	count := 0
	if err := scanValue(decoder, "$", 1, options, &count); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return structuralError("$", "TRAILING_JSON", "a second JSON value %v is not allowed", token)
		}
		return structuralError("$", "INVALID_JSON", "%v", err)
	}
	return nil
}

// A tiny local reader keeps this package independent of the rest of the JSON
// compiler implementation while still allowing Decoder to stream input.
type byteReader struct {
	data   []byte
	offset int
}

func bytesReader(data []byte) *byteReader { return &byteReader{data: data} }
func (r *byteReader) Read(out []byte) (int, error) {
	if r.offset == len(r.data) {
		return 0, io.EOF
	}
	n := copy(out, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func scanValue(decoder *json.Decoder, path string, depth int, options Options, count *int) error {
	if options.MaxDepth > 0 && depth > options.MaxDepth {
		return structuralError(path, "DEPTH_LIMIT", "JSON depth exceeds %d", options.MaxDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return structuralError(path, "INVALID_JSON", "%v", err)
	}
	if token == nil {
		return structuralError(path, "NULL_NOT_ALLOWED", "null is not allowed")
	}
	if options.MaxValues > 0 {
		if _, composite := token.(json.Delim); !composite {
			(*count)++
			if *count > options.MaxValues {
				return structuralError(path, "VALUE_LIMIT", "JSON contains more than %d values", options.MaxValues)
			}
		}
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			seen := map[string]struct{}{}
			children := 0
			for decoder.More() {
				children++
				if options.MaxObjectFields > 0 && children > options.MaxObjectFields {
					return structuralError(path, "OBJECT_FIELD_LIMIT", "object contains more than %d fields", options.MaxObjectFields)
				}
				keyToken, err := decoder.Token()
				if err != nil {
					return structuralError(path, "INVALID_JSON", "%v", err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return structuralError(path, "INVALID_JSON", "object key is not a string")
				}
				if options.MaxStringBytes > 0 && len(key) > options.MaxStringBytes {
					return structuralError(path+"."+key, "STRING_SIZE_LIMIT", "string contains %d bytes; maximum is %d", len(key), options.MaxStringBytes)
				}
				if _, exists := seen[key]; exists {
					return structuralError(path+"."+key, "DUPLICATE_KEY", "object key %q is duplicated", key)
				}
				seen[key] = struct{}{}
				if err := scanValue(decoder, path+"."+key, depth+1, options, count); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return structuralError(path, "INVALID_JSON", "object is not closed")
			}
		case '[':
			index := 0
			for decoder.More() {
				if options.MaxArrayItems > 0 && index+1 > options.MaxArrayItems {
					return structuralError(path, "ARRAY_ITEM_LIMIT", "array contains more than %d items", options.MaxArrayItems)
				}
				if err := scanValue(decoder, fmt.Sprintf("%s[%d]", path, index), depth+1, options, count); err != nil {
					return err
				}
				index++
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return structuralError(path, "INVALID_JSON", "array is not closed")
			}
		default:
			return fmt.Errorf("%s has an unexpected delimiter %q", path, value)
		}
	case string:
		if options.MaxStringBytes > 0 && len(value) > options.MaxStringBytes {
			return structuralError(path, "STRING_SIZE_LIMIT", "string contains %d bytes; maximum is %d", len(value), options.MaxStringBytes)
		}
	}
	return nil
}
