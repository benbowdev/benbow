// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package keychain stores secrets as generic password items in a macOS
// keychain through /usr/bin/security. Secrets never travel as arguments:
// they are written hex-encoded on the stdin of `security -i` (so any byte is
// allowed, quotes and backslashes included) and read back from the `-g`
// output.
package keychain

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

// ErrNotFound means the keychain holds no matching item.
var ErrNotFound = errors.New("no such item in the keychain")

// Runner executes /usr/bin/security with stdin and returns its combined
// output.
type Runner func(ctx context.Context, stdin string, args ...string) (string, error)

// Item is one generic password: Service plus Account in the keychain file
// at Path.
type Item struct {
	Path    string // keychain file
	Service string
	Account string
	// Label is what Keychain Access shows; Service when empty.
	Label string
	// Run executes /usr/bin/security; overridable in tests.
	Run Runner
}

// LoginPath is the user's login keychain.
func LoginPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Keychains", "login.keychain-db"), nil
}

// Login returns the item service/account in the login keychain.
func Login(service, account string) (Item, error) {
	p, err := LoginPath()
	return Item{Path: p, Service: service, Account: account}, err
}

func (i Item) run(ctx context.Context, stdin string, args ...string) (string, error) {
	if i.Run != nil {
		return i.Run(ctx, stdin, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// quotable reports whether s can sit between double quotes on a
// `security -i` line.
func quotable(s string) bool { return !strings.ContainsAny(s, "\"\\\n\r\x00") }

// Set stores secret, replacing any previous one, and reads it back (`security
// -i` exits 0 even when a command fails).
func (i Item) Set(ctx context.Context, secret string) error {
	if secret == "" {
		return errcode.Errorf(errcode.InvalidInput, "empty secret")
	}
	label := i.Label
	if label == "" {
		label = i.Service
	}
	if i.Service == "" || i.Account == "" || !quotable(i.Service+i.Account+label+i.Path) {
		return errcode.Errorf(errcode.InvalidInput, "unsupported keychain service, account, label or path")
	}
	line := fmt.Sprintf("add-generic-password -U -a \"%s\" -s \"%s\" -l \"%s\" -j \"Created by Covo. Removed by covo uninstall.\" -X %s \"%s\"\n",
		i.Account, i.Service, label, hex.EncodeToString([]byte(secret)), i.Path)
	if out, err := i.run(ctx, line, "-i"); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("store %s in the keychain: %w: %s", i.Service, err, strings.TrimSpace(out)))
	}
	got, err := i.Get(ctx)
	if err != nil {
		return err
	}
	if got != secret {
		return errcode.Errorf(errcode.Service, "the keychain did not store "+i.Service)
	}
	return nil
}

// reHexSecret and reTextSecret match the secret line of `-g`: hex when it
// has bytes that are not printable, text between quotes otherwise.
var (
	reHexSecret  = regexp.MustCompile(`(?m)^password: 0x([0-9A-Fa-f]*)`)
	reTextSecret = regexp.MustCompile(`(?m)^password: "(.*)"$`)
)

// Get returns the stored secret, or ErrNotFound.
func (i Item) Get(ctx context.Context) (string, error) {
	out, err := i.run(ctx, "", "find-generic-password", "-a", i.Account, "-s", i.Service, "-g", i.Path)
	if err != nil {
		if strings.Contains(out, "could not be found") {
			return "", ErrNotFound
		}
		return "", errcode.New(errcode.Service, fmt.Errorf("read %s from the keychain: %w: %s", i.Service, err, strings.TrimSpace(out)))
	}
	return parseSecret(out)
}

func parseSecret(out string) (string, error) {
	if m := reHexSecret.FindStringSubmatch(out); m != nil {
		b, err := hex.DecodeString(m[1])
		if err != nil {
			return "", errcode.New(errcode.Service, fmt.Errorf("decode keychain secret: %w", err))
		}
		return string(b), nil
	}
	if m := reTextSecret.FindStringSubmatch(out); m != nil {
		return m[1], nil
	}
	return "", errcode.Errorf(errcode.Service, "unexpected output from security find-generic-password")
}

// Exists reports whether the item is stored.
func (i Item) Exists(ctx context.Context) (bool, error) {
	_, err := i.Get(ctx)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Delete removes the item. It is a no-op if there is none.
func (i Item) Delete(ctx context.Context) (bool, error) {
	out, err := i.run(ctx, "", "delete-generic-password", "-a", i.Account, "-s", i.Service, i.Path)
	if err != nil {
		if strings.Contains(out, "could not be found") {
			return false, nil
		}
		return false, errcode.New(errcode.Service, fmt.Errorf("remove %s from the keychain: %w: %s", i.Service, err, strings.TrimSpace(out)))
	}
	return true, nil
}

// DeleteAll removes every item under Service, whatever the account, and
// returns how many were removed.
func (i Item) DeleteAll(ctx context.Context) (int, error) {
	for n := 0; n < 10000; n++ {
		out, err := i.run(ctx, "", "delete-generic-password", "-s", i.Service, i.Path)
		if err != nil {
			if strings.Contains(out, "could not be found") {
				return n, nil
			}
			return n, errcode.New(errcode.Service, fmt.Errorf("remove %s items from the keychain: %w: %s", i.Service, err, strings.TrimSpace(out)))
		}
	}
	return 0, errcode.Errorf(errcode.Service, "too many "+i.Service+" items in the keychain")
}

// AnyExists reports whether any item is stored under Service.
func (i Item) AnyExists(ctx context.Context) (bool, error) {
	out, err := i.run(ctx, "", "find-generic-password", "-s", i.Service, i.Path)
	if err != nil {
		if strings.Contains(out, "could not be found") {
			return false, nil
		}
		return false, errcode.New(errcode.Service, fmt.Errorf("search the keychain: %w: %s", err, strings.TrimSpace(out)))
	}
	return true, nil
}
