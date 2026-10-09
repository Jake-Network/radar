// Package jsonptr implements the subset of RFC 6901 JSON pointers Radar needs.
package jsonptr

import (
	"errors"
	"strconv"
	"strings"
)

// Validate checks pointer syntax: empty, or "/"-prefixed with valid ~ escapes.
func Validate(ptr string) error {
	if ptr == "" {
		return nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return errors.New("JSON pointer must be empty or start with /")
	}
	for i := 0; i < len(ptr); i++ {
		if ptr[i] == '~' {
			if i+1 >= len(ptr) || (ptr[i+1] != '0' && ptr[i+1] != '1') {
				return errors.New("invalid RFC 6901 pointer escape")
			}
			i++
		}
	}
	return nil
}

// Tokens splits and unescapes a valid pointer.
func Tokens(ptr string) ([]string, error) {
	if err := Validate(ptr); err != nil {
		return nil, err
	}
	if ptr == "" {
		return nil, nil
	}
	parts := strings.Split(ptr[1:], "/")
	for i, p := range parts {
		parts[i] = Unescape(p)
	}
	return parts, nil
}

// Escape encodes one reference token.
func Escape(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

// Unescape decodes one reference token.
func Unescape(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
}

// ErrNonContainer reports a pointer that crosses a scalar value.
var ErrNonContainer = errors.New("JSON pointer crosses a non-container value")

// Lookup resolves ptr in a decoded JSON document. found is false when a member
// or index is absent; an error is returned for malformed pointers or when the
// pointer crosses a scalar.
func Lookup(doc any, ptr string) (value any, found bool, err error) {
	tokens, err := Tokens(ptr)
	if err != nil {
		return nil, false, err
	}
	cur := doc
	for _, token := range tokens {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[token]
			if !ok {
				return nil, false, nil
			}
			cur = next
		case []any:
			i, convErr := strconv.Atoi(token)
			if convErr != nil || i < 0 || strconv.Itoa(i) != token {
				return nil, false, nil
			}
			if i >= len(v) {
				return nil, false, nil
			}
			cur = v[i]
		default:
			return nil, false, ErrNonContainer
		}
	}
	return cur, true, nil
}
