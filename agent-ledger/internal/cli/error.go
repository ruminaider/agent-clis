package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Status values for the error envelope.
const (
	StatusOK     = "ok"
	StatusError  = "error"
	StatusFailed = "failed"
)

// Error is the canonical command error. It carries an exit code, a stable
// machine-readable code string, a human message, and optional structured
// details. Cobra's RunE returns an *Error so the top-level runner can render
// the envelope and exit with the matching status.
type Error struct {
	ExitCode int            `json:"-"`
	Status   string         `json:"status"`
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Details  map[string]any `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// NewError constructs an Error with the given exit code and machine code.
func NewError(exitCode int, code, message string) *Error {
	return &Error{
		ExitCode: exitCode,
		Status:   StatusError,
		Code:     code,
		Message:  message,
	}
}

// WithDetails attaches structured details to an Error and returns it.
func (e *Error) WithDetails(details map[string]any) *Error {
	if e == nil {
		return nil
	}
	if len(details) == 0 {
		return e
	}
	if e.Details == nil {
		e.Details = make(map[string]any, len(details))
	}
	for k, v := range details {
		e.Details[k] = v
	}
	return e
}

// NotImplemented returns the standard error for stub Phase 1 commands.
func NotImplemented(command string) *Error {
	return NewError(
		ExitNotImplemented,
		"not_implemented",
		fmt.Sprintf("%q is not implemented yet", command),
	).WithDetails(map[string]any{"command": command})
}

// WriteJSON renders the envelope as a single-line JSON object to w.
func (e *Error) WriteJSON(w io.Writer) error {
	if e == nil {
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(e)
}

// WriteText renders the envelope as a single human-readable line to w.
func (e *Error) WriteText(w io.Writer) error {
	if e == nil {
		return nil
	}
	var b strings.Builder
	b.WriteString(strings.ToUpper(e.Status))
	b.WriteString(": ")
	if e.Code != "" {
		b.WriteString("[")
		b.WriteString(e.Code)
		b.WriteString("] ")
	}
	b.WriteString(e.Message)
	if len(e.Details) > 0 {
		raw, err := json.Marshal(e.Details)
		if err == nil {
			b.WriteString(" details=")
			b.Write(raw)
		}
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}
