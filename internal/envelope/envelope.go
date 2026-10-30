// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package envelope is the stable JSON shape shared by `covo --json` and the
// local API, so every client parses the same document.
package envelope

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/getcovo/covo/internal/errcode"
)

// SchemaVersion is the version of the envelope. Bump it only on a breaking
// change to the envelope itself or to an existing command's data.
const SchemaVersion = 1

// Envelope is the stable shape of every JSON response.
type Envelope struct {
	Schema int        `json:"schema"`
	OK     bool       `json:"ok"`
	Data   any        `json:"data,omitempty"`
	Error  *ErrorBody `json:"error,omitempty"`
}

// ErrorBody is the error part of the envelope.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// OK wraps data in a successful envelope.
func OK(data any) Envelope {
	return Envelope{Schema: SchemaVersion, OK: true, Data: data}
}

// Error wraps err in a failed envelope with its stable code.
func Error(err error) Envelope {
	return Envelope{Schema: SchemaVersion, Error: &ErrorBody{Code: errcode.Code(err), Message: err.Error()}}
}

// Write encodes v as indented JSON.
func Write(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	return nil
}
