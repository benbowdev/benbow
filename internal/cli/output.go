// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"fmt"
	"io"

	"github.com/getcovo/covo/internal/envelope"
)

// Result is what a command returns: it is serialised as the envelope's data
// in --json mode and printed with Text otherwise.
type Result interface {
	Text(w io.Writer)
}

func writeResult(w io.Writer, asJSON bool, r Result) error {
	if !asJSON {
		r.Text(w)
		return nil
	}
	return envelope.Write(w, envelope.OK(r))
}

func writeError(w io.Writer, asJSON bool, err error) {
	if !asJSON {
		fmt.Fprintf(w, "Error: %v\n", err)
		return
	}
	_ = envelope.Write(w, envelope.Error(err))
}
