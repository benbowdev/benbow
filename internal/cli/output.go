// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/getcovo/covo/internal/errcode"
)

// SchemaVersion is the version of the --json envelope. Bump it only on a
// breaking change to the envelope itself or to an existing command's data.
const SchemaVersion = 1

// Envelope is the stable shape of every --json response.
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

// Result is what a command returns: it is serialised as Envelope.Data in
// --json mode and printed with Text otherwise.
type Result interface {
	Text(w io.Writer)
}

func writeResult(w io.Writer, asJSON bool, r Result) error {
	if !asJSON {
		r.Text(w)
		return nil
	}
	return writeJSON(w, Envelope{Schema: SchemaVersion, OK: true, Data: r})
}

func writeError(w io.Writer, asJSON bool, err error) {
	if !asJSON {
		fmt.Fprintf(w, "Error: %v\n", err)
		return
	}
	_ = writeJSON(w, Envelope{
		Schema: SchemaVersion,
		Error:  &ErrorBody{Code: errcode.Code(err), Message: err.Error()},
	})
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	return nil
}
