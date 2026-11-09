// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/remote"
	"github.com/benbowdev/benbow/internal/store"
)

// A host or user starting with "-" would reach ssh as an option
// (-oProxyCommand=… runs a command).
func TestSetRemoteRejectsOptionLikeNames(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	if err := c.Store.AddSite(ctx, store.Site{Name: "shop", Path: t.TempDir(), PHPMinor: "8.4", Domain: "shop.localhost", Enabled: true, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	base := store.Remote{Site: "shop", Kind: RemoteProduction, Transport: remote.SSH, Host: "example.com", User: "deploy", Path: "/var/www", URL: "https://example.com"}
	tests := []struct {
		name       string
		host, user string
	}{
		{"user option", "example.com", "-oProxyCommand=touch${IFS}x;#"},
		{"host option", "-oProxyCommand=x", "deploy"},
		{"host dash", "-v", "deploy"},
		{"user with @", "example.com", "a@b"},
		{"user with newline", "example.com", "a\nb"},
	}
	for _, tt := range tests {
		r := base
		r.Host, r.User = tt.host, tt.user
		if _, err := c.SetRemote(ctx, r, nil); errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("%s: SetRemote = %v, want invalid_input", tt.name, err)
		}
	}
}
