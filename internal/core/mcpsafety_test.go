// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

func TestRunAgentChange(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	ran := 0
	fn := func() error { ran++; return nil }

	// Read-only by default: nothing runs, the refusal is logged.
	if _, err := c.RunAgentChange(ctx, AgentChange{Tool: "add_site", Site: "shop", Args: `{"name":"shop"}`}, fn); errcode.Code(err) != errcode.Forbidden {
		t.Fatalf("without permission = %v, want forbidden", err)
	}
	if ran != 0 {
		t.Fatal("the change ran without permission")
	}

	if _, err := c.SetMCPPermission(ctx, MCPAll, true); err != nil {
		t.Fatal(err)
	}
	// Allowed; the site does not exist yet, so there is no session.
	res, err := c.RunAgentChange(ctx, AgentChange{Tool: "add_site", Site: "shop", Args: `{"name":"shop"}`}, fn)
	if err != nil || ran != 1 || res.SessionStart != "" || res.Checkpoint != "" {
		t.Fatalf("allowed change = %+v, %v (ran %d)", res, err, ran)
	}
	// A failing change is logged with its error.
	boom := errors.New("boom")
	if _, err := c.RunAgentChange(ctx, AgentChange{Tool: "wp", Site: "", DryRun: true}, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("failing change = %v", err)
	}

	acts, err := c.MCPActions(ctx, 10)
	if err != nil || len(acts) != 3 {
		t.Fatalf("actions = %+v, %v", acts, err)
	}
	byTool := map[string]int{}
	for _, a := range acts {
		byTool[a.Tool]++
		if a.Tool == "wp" && (a.OK || a.Error != "boom" || !a.DryRun) {
			t.Errorf("wp action = %+v", a)
		}
	}
	if byTool["add_site"] != 2 {
		t.Errorf("actions by tool = %v", byTool)
	}
}
