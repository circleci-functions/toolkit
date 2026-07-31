/*
Package secret provides string alias types for redacting sensitive
information when stringified.

This package only protects against leaks, and is NOT meant to
store secure information in memory. Do not use this package on code
that runs in untrusted environments.

It does this by storing the secrets as pointers. When possible,
the output of a secret is **REDACTED SECRET**. When not possible
(using %v on an unexported field), the output of a secret will be its address.
So if you see a string with an address, don't panic it is not a secret.
*/
package secret

import (
	"encoding/json"
	"os"
)

// Redacted is a placeholder and sensitive secrets are replaced by it.
const Redacted = "**REDACTED SECRET**"

// String represents a secret string. Its content is stored as a pointer
// to prevent leaks (Go would print the address instead of the value),
// and cannot be accessed by any other mean than calling
// Value().
type String struct {
	value *string
}

// NewString creates a new secret string.
func NewString(secret string) String {
	return String{value: &secret}
}

// FromEnv gets secret string from environment (function may be removed
// in future when we add the backends notion).
func FromEnv(name string) (String, bool) {
	val, ok := os.LookupEnv(name)
	if !ok {
		return String{}, false
	}
	return String{value: &val}, true
}

// EnvDecode Add support for custom decoder for go-envconfig lib
func (s *String) EnvDecode(secret string) error {
	s.value = &secret
	return nil
}

// Value returns the sensitive value as a string.
func (s String) Value() string {
	if s.value == nil {
		return ""
	}
	return *s.value
}

// String implements fmt.Stringer and redacts the sensitive value.
func (s String) String() string {
	return Redacted
}

// GoString implements fmt.GoStringer and redacts the sensitive value.
func (s String) GoString() string {
	return Redacted
}

// MarshalJSON implements json.MarshalJSON and redacts the sensitive value.
func (s String) MarshalJSON() ([]byte, error) {
	return []byte(`"` + Redacted + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler and sets the sensitive value.
func (s *String) UnmarshalJSON(b []byte) error {
	var str string
	err := json.Unmarshal(b, &str)
	if err != nil {
		return err
	}

	s.value = &str
	return nil
}

// UnmarshalText implements encoding.TextUnmarshaler and sets the sensitive value.
func (s *String) UnmarshalText(b []byte) error {
	str := string(b)
	s.value = &str
	return nil
}
