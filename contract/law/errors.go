package law

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidLimits marks invalid host-selected bounds, checked before input.
	ErrInvalidLimits = errors.New("law: invalid load limits")
	// ErrLimit marks an input exceeding a parser bound; see LimitError.
	ErrLimit = errors.New("law: load limit exceeded")
	// ErrMalformed marks non-strict JSON; see MalformedError.
	ErrMalformed = errors.New("law: not strict JSON")
	// ErrUnsupported marks an unrecognized format, version or kernel.
	ErrUnsupported = errors.New("law: unsupported artifact")
	// ErrInvalid marks a version-1 field violation; see FieldError.
	ErrInvalid = errors.New("law: invalid artifact")
)

// Bound is bytes, depth, members, elements, string_bytes or tokens.
type Bound string

// Problem is bom, invalid_utf8, syntax, duplicate_key, not_unsigned_integer
// or trailing_data.
type Problem string

// Violation is unknown_field, missing_field, wrong_type, unknown_value,
// out_of_range, pattern, count, unsorted, duplicate or mismatch.
type Violation string

// LimitError locates an exceeded inclusive parser bound. Offset is an input
// byte offset; for bytes it is Max.
type LimitError struct {
	Bound  Bound
	Max    int
	Offset int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%v: %s above %d at %d", ErrLimit, e.Bound, e.Max, e.Offset)
}
func (e *LimitError) Unwrap() error { return ErrLimit }

// MalformedError classifies a strict-JSON refusal. Offset is an input byte
// offset (zero for a BOM), not a stable diagnostic location across versions.
type MalformedError struct {
	Problem Problem
	Offset  int64
}

func (e *MalformedError) Error() string {
	return fmt.Sprintf("%v: %s at %d", ErrMalformed, e.Problem, e.Offset)
}
func (e *MalformedError) Unwrap() error { return ErrMalformed }

// UnsupportedError reports a head value without interpreting it as version 1.
// Field is format, schema_version, evaluator_version or kernel. Value and
// Supported are decoded strings or integer digits, never rounded numbers.
type UnsupportedError struct {
	Field     string
	Value     string
	Supported string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%v: %s=%q, supports %q", ErrUnsupported, e.Field, e.Value, e.Supported)
}
func (e *UnsupportedError) Unwrap() error { return ErrUnsupported }

// FieldError locates a version-1 violation by RFC 6901 JSON Pointer. The empty
// Path names the root. Error text is not a compatibility contract.
type FieldError struct {
	Path      string
	Violation Violation
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%v: %s at %q", ErrInvalid, e.Violation, e.Path)
}
func (e *FieldError) Unwrap() error { return ErrInvalid }
