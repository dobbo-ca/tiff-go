// Package e holds the error values shared by every stage.
package e

import (
	"errors"
	"fmt"
)

var (
	// ErrLimit is wrapped by every *LimitError.
	ErrLimit = errors.New("tiff: limit exceeded")
	// ErrCorrupt marks structurally invalid input.
	ErrCorrupt = errors.New("tiff: corrupt")
	// ErrUnsupported marks valid TIFF this package does not decode.
	ErrUnsupported = errors.New("tiff: unsupported")
)

// LimitError reports which cap tripped and by how much. Names are fixed
// strings; no input content is ever echoed.
type LimitError struct {
	Name     string
	Got, Max int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("tiff: limit exceeded: %s %d > %d", e.Name, e.Got, e.Max)
}

func (e *LimitError) Unwrap() error { return ErrLimit }

// Corrupt wraps ErrCorrupt with a message built from numbers and tag ids only.
func Corrupt(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrCorrupt}, a...)...)
}

// Unsupported wraps ErrUnsupported likewise.
func Unsupported(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrUnsupported}, a...)...)
}
