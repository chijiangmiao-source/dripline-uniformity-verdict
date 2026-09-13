package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
)

// errJSONSyntax marks a structurally invalid JSON document. It carries no
// detail on purpose: callers map it to the same generic 400 the standard
// library decoder used to produce.
var errJSONSyntax = errors.New("invalid JSON document")

// duplicateFieldError reports that one object repeated a member name; key is
// the first name seen twice. It is only produced for the object levels the
// request contract unpacks — values kept as raw text keep encoding/json's
// last-wins behaviour. The body is syntactically valid JSON, so callers map
// it to a located 422 (the repeated field) rather than a generic 400.
type duplicateFieldError struct{ key string }

func (e *duplicateFieldError) Error() string {
	return fmt.Sprintf("duplicate object member %q", e.key)
}

// The scanner below accepts exactly one extension over strict JSON: number
// tokens may carry leading zeros (e.g. 01). A flow submitted that way is not
// valid JSON, but rejecting the whole body as malformed would lose the field
// location; the token instead reaches field-level validation, which rejects
// it with a 422 naming the flow field. Every other production — string
// escapes, structural characters, literals, exponents — follows the JSON
// grammar exactly, mirroring encoding/json.

// parseJSONObject parses raw as a JSON object and returns each member's raw
// value. The null literal decodes to a nil map without error, mirroring
// json.Unmarshal into map[string]json.RawMessage. One behaviour diverges from
// encoding/json on purpose: a repeated member name is rejected with a
// duplicateFieldError, because the contract reads members at this level and
// last-wins would silently discard one of the two values.
func parseJSONObject(raw []byte) (map[string]json.RawMessage, error) {
	s := &jsonScanner{data: raw}
	s.skipSpace()
	if s.off >= len(s.data) {
		return nil, errJSONSyntax
	}
	if s.data[s.off] == 'n' {
		if err := s.literal("null"); err != nil {
			return nil, err
		}
		if s.skipSpace(); s.off != len(s.data) {
			return nil, errJSONSyntax
		}
		return nil, nil
	}
	if s.data[s.off] != '{' {
		return nil, errJSONSyntax
	}
	members, err := s.strictObject()
	if err != nil {
		return nil, err
	}
	if s.skipSpace(); s.off != len(s.data) {
		return nil, errJSONSyntax
	}
	return members, nil
}

// parseJSONArray parses raw as a JSON array and returns each element's raw
// value, mirroring json.Unmarshal into []json.RawMessage — including the null
// literal decoding to a nil slice without error.
func parseJSONArray(raw []byte) ([]json.RawMessage, error) {
	s := &jsonScanner{data: raw}
	s.skipSpace()
	if s.off >= len(s.data) {
		return nil, errJSONSyntax
	}
	if s.data[s.off] == 'n' {
		if err := s.literal("null"); err != nil {
			return nil, err
		}
		if s.skipSpace(); s.off != len(s.data) {
			return nil, errJSONSyntax
		}
		return nil, nil
	}
	if s.data[s.off] != '[' {
		return nil, errJSONSyntax
	}
	items, err := s.array()
	if err != nil {
		return nil, err
	}
	if s.skipSpace(); s.off != len(s.data) {
		return nil, errJSONSyntax
	}
	return items, nil
}

// maxJSONDepth mirrors encoding/json's nesting limit so adversarial bodies
// cannot overflow the stack of the recursive scanner.
const maxJSONDepth = 10000

type jsonScanner struct {
	data  []byte
	off   int
	depth int
}

func (s *jsonScanner) skipSpace() {
	for s.off < len(s.data) {
		switch s.data[s.off] {
		case ' ', '\t', '\n', '\r':
			s.off++
		default:
			return
		}
	}
}

// value validates one JSON value and returns its raw bytes. Objects and
// arrays are validated recursively but kept as raw text; only the levels the
// request contract cares about are unpacked by parseJSONObject/parseJSONArray.
func (s *jsonScanner) value() (json.RawMessage, error) {
	s.skipSpace()
	if s.off >= len(s.data) {
		return nil, errJSONSyntax
	}
	start := s.off
	switch c := s.data[s.off]; {
	case c == '{':
		if _, err := s.object(); err != nil {
			return nil, err
		}
	case c == '[':
		if _, err := s.array(); err != nil {
			return nil, err
		}
	case c == '"':
		if err := s.str(); err != nil {
			return nil, err
		}
	case c == 't':
		if err := s.literal("true"); err != nil {
			return nil, err
		}
	case c == 'f':
		if err := s.literal("false"); err != nil {
			return nil, err
		}
	case c == 'n':
		if err := s.literal("null"); err != nil {
			return nil, err
		}
	case c == '-' || (c >= '0' && c <= '9'):
		if err := s.number(); err != nil {
			return nil, err
		}
	default:
		return nil, errJSONSyntax
	}
	return s.data[start:s.off], nil
}

func (s *jsonScanner) object() (map[string]json.RawMessage, error) {
	return s.parseObject(false)
}

// strictObject parses one object like object but rejects a repeated member
// name with a duplicateFieldError. Only the levels the contract unpacks use
// it; nested objects reached through value stay permissive.
func (s *jsonScanner) strictObject() (map[string]json.RawMessage, error) {
	return s.parseObject(true)
}

func (s *jsonScanner) parseObject(rejectDups bool) (map[string]json.RawMessage, error) {
	s.depth++
	if s.depth > maxJSONDepth {
		return nil, errJSONSyntax
	}
	defer func() { s.depth-- }()

	s.off++ // consume '{'
	members := make(map[string]json.RawMessage)
	s.skipSpace()
	if s.off < len(s.data) && s.data[s.off] == '}' {
		s.off++
		return members, nil
	}
	for {
		s.skipSpace()
		if s.off >= len(s.data) || s.data[s.off] != '"' {
			return nil, errJSONSyntax
		}
		keyStart := s.off
		if err := s.str(); err != nil {
			return nil, err
		}
		var key string
		// The key was just validated as a well-formed JSON string, so this
		// decode cannot fail; it resolves escapes exactly like encoding/json.
		if err := json.Unmarshal(s.data[keyStart:s.off], &key); err != nil {
			return nil, errJSONSyntax
		}
		s.skipSpace()
		if s.off >= len(s.data) || s.data[s.off] != ':' {
			return nil, errJSONSyntax
		}
		s.off++
		val, err := s.value()
		if err != nil {
			return nil, err
		}
		if rejectDups {
			if _, repeated := members[key]; repeated {
				return nil, &duplicateFieldError{key: key}
			}
		}
		members[key] = val
		s.skipSpace()
		if s.off >= len(s.data) {
			return nil, errJSONSyntax
		}
		if s.data[s.off] == ',' {
			s.off++
			continue
		}
		if s.data[s.off] == '}' {
			s.off++
			return members, nil
		}
		return nil, errJSONSyntax
	}
}

func (s *jsonScanner) array() ([]json.RawMessage, error) {
	s.depth++
	if s.depth > maxJSONDepth {
		return nil, errJSONSyntax
	}
	defer func() { s.depth-- }()

	s.off++ // consume '['
	items := []json.RawMessage{}
	s.skipSpace()
	if s.off < len(s.data) && s.data[s.off] == ']' {
		s.off++
		return items, nil
	}
	for {
		item, err := s.value()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		s.skipSpace()
		if s.off >= len(s.data) {
			return nil, errJSONSyntax
		}
		if s.data[s.off] == ',' {
			s.off++
			continue
		}
		if s.data[s.off] == ']' {
			s.off++
			return items, nil
		}
		return nil, errJSONSyntax
	}
}

// str validates a JSON string starting at the opening quote. Escape handling
// and the rejection of unescaped control characters match encoding/json.
func (s *jsonScanner) str() error {
	s.off++ // consume the opening quote
	for s.off < len(s.data) {
		c := s.data[s.off]
		switch {
		case c == '"':
			s.off++
			return nil
		case c == '\\':
			s.off++
			if s.off >= len(s.data) {
				return errJSONSyntax
			}
			switch s.data[s.off] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				s.off++
			case 'u':
				s.off++
				for i := 0; i < 4; i++ {
					if s.off >= len(s.data) || !isHexDigit(s.data[s.off]) {
						return errJSONSyntax
					}
					s.off++
				}
			default:
				return errJSONSyntax
			}
		case c < 0x20:
			return errJSONSyntax
		default:
			s.off++
		}
	}
	return errJSONSyntax
}

// number validates a JSON number token. Unlike the strict grammar, the integer
// part may carry leading zeros: that is the one tolerated extension, and the
// field validators downstream reject such values with a located 422.
func (s *jsonScanner) number() error {
	if s.off < len(s.data) && s.data[s.off] == '-' {
		s.off++
	}
	if !s.digits() {
		return errJSONSyntax
	}
	if s.off < len(s.data) && s.data[s.off] == '.' {
		s.off++
		if !s.digits() {
			return errJSONSyntax
		}
	}
	if s.off < len(s.data) && (s.data[s.off] == 'e' || s.data[s.off] == 'E') {
		s.off++
		if s.off < len(s.data) && (s.data[s.off] == '+' || s.data[s.off] == '-') {
			s.off++
		}
		if !s.digits() {
			return errJSONSyntax
		}
	}
	return nil
}

func (s *jsonScanner) digits() bool {
	start := s.off
	for s.off < len(s.data) && s.data[s.off] >= '0' && s.data[s.off] <= '9' {
		s.off++
	}
	return s.off > start
}

func (s *jsonScanner) literal(word string) error {
	if len(s.data)-s.off < len(word) || string(s.data[s.off:s.off+len(word)]) != word {
		return errJSONSyntax
	}
	s.off += len(word)
	return nil
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
