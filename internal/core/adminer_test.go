// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"strings"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
)

func TestAdminerLoginIsOneTime(t *testing.T) {
	c := &Core{adminerLogins: map[string]adminerLogin{
		"good":    {creds: AdminerCredentials{Username: "root"}, expires: time.Now().Add(time.Minute)},
		"expired": {creds: AdminerCredentials{Username: "root"}, expires: time.Now().Add(-time.Second)},
	}}
	if got, err := c.RedeemAdminerLogin("good"); err != nil || got.Username != "root" {
		t.Fatalf("first redeem = %+v, %v", got, err)
	}
	for _, tok := range []string{"good", "expired", "never"} {
		if _, err := c.RedeemAdminerLogin(tok); errcode.Code(err) != errcode.NotFound {
			t.Errorf("redeem %s err = %v, want not_found", tok, err)
		}
	}
}

func TestAdminerFront(t *testing.T) {
	front, err := adminerFront("/Users/x/Library/Application Support/benbow/components/adminer/adminer.php", "/Users/x/Library/Application Support/benbow/run/benbow.sock")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CURLOPT_UNIX_SOCKET_PATH => '/Users/x/Library/Application Support/benbow/run/benbow.sock'",
		"require '/Users/x/Library/Application Support/benbow/components/adminer/adminer.php';", "[0-9a-f]{64}"} {
		if !strings.Contains(front, want) {
			t.Errorf("front file lacks %q", want)
		}
	}
	if _, err := adminerFront("/x/it's/adminer.php", "/x/benbow.sock"); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("quote in path err = %v", err)
	}
}
