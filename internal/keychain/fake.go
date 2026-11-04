// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package keychain

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Fake is an in-memory `security` for tests: it understands the commands
// Item sends and records how it was called. Use Fake.Run as Item.Run.
type Fake struct {
	Items map[string]string // "account@service" → secret
	Calls [][]string
	Stdin []string
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{Items: map[string]string{}} }

const missing = "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain."

// Run implements Runner.
func (f *Fake) Run(_ context.Context, stdin string, args ...string) (string, error) {
	f.Calls = append(f.Calls, args)
	f.Stdin = append(f.Stdin, stdin)
	notFound := errors.New("exit status 44")
	if args[0] == "-i" {
		acct, svc, secret := quoted(stdin, "-a"), quoted(stdin, "-s"), field(stdin, "-X")
		b, err := hex.DecodeString(secret)
		if err != nil {
			return "", fmt.Errorf("fake security: %w", err)
		}
		f.Items[acct+"@"+svc] = string(b)
		return "", nil
	}
	acct, svc := flag(args, "-a"), flag(args, "-s")
	match := ""
	for key := range f.Items {
		a, s, _ := strings.Cut(key, "@")
		if s == svc && (acct == "" || a == acct) {
			match = key
			break
		}
	}
	if match == "" {
		return missing, notFound
	}
	switch args[0] {
	case "find-generic-password":
		return "keychain: \"x\"\npassword: 0x" + strings.ToUpper(hex.EncodeToString([]byte(f.Items[match]))) + "  \"...\"\n", nil
	case "delete-generic-password":
		delete(f.Items, match)
		return "", nil
	}
	return "", errors.New("unexpected")
}

func flag(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// quoted returns the double-quoted value after name on a `security -i` line.
func quoted(line, name string) string {
	_, rest, ok := strings.Cut(line, " "+name+" \"")
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, "\"")
	return v
}

func field(line, name string) string {
	return flag(strings.Fields(line), name)
}
