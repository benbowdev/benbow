// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package errcode attaches stable, machine-readable codes to errors so that
// every client (CLI --json, later the local API and MCP) reports the same code
// for the same failure.
package errcode

import "errors"

// Stable error codes. They are part of the public --json schema: never rename
// one, only add new ones.
const (
	Internal     = "internal"
	InvalidInput = "invalid_input"
	NotFound     = "not_found"
	Conflict     = "conflict"
	PortInUse    = "port_in_use"
	Checksum     = "checksum_mismatch"
	Download     = "download_failed"
	Locked       = "locked"
	Service      = "service_failed"
	Unsupported  = "unsupported"
	// ForbiddenFolder: a site folder that must never be served (home, disk
	// root, system folders, the Covo home).
	ForbiddenFolder = "forbidden_folder"
	// ConfirmationRequired: the action needs explicit confirmation (--force).
	ConfirmationRequired = "confirmation_required"
)

// Error is an error carrying a stable code.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// New wraps err with code. It returns nil if err is nil.
func New(code string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// Errorf is a shorthand for New(code, errors.New(msg)).
func Errorf(code, msg string) error {
	return &Error{Code: code, Err: errors.New(msg)}
}

// Code returns the code of the first *Error in err's chain, or Internal.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Internal
}
