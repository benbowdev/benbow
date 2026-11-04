// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/agents"
	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/envelope"
	"github.com/getcovo/covo/internal/store"
)

var update = flag.Bool("update", false, "rewrite the wire golden files")

// wireDir holds the JSON the app decodes: the Swift tests read the same
// files (CovoKitTests, testWireFixtures), so a renamed or retyped field
// shows up on both sides.
const wireDir = "../../app/Tests/CovoKitTests/Fixtures/wire"

// wireTypes are the answers the app decodes, by file name. Add one when the
// app starts decoding a new answer, and its Swift type in CovoKitTests.
var wireTypes = map[string]any{
	"status":            core.Status{},
	"site-result":       core.SiteResult{},
	"site-database":     core.SiteDatabase{},
	"wordpress-result":  core.WordPressResult{},
	"laravel-result":    core.LaravelResult{},
	"tool-result":       core.ToolResult{},
	"site-php-settings": core.SitePHPSettings{},
	"site-log":          core.SiteLog{},
	"updates":           core.UpdatesStatus{},
	"php-list":          core.PHPList{},
	"php-settings":      core.PHPSettings{},
	"xdebug":            []core.XdebugStatus{},
	"mysql-credentials": core.MySQLCredentials{},
	"import-plan":       core.ImportPlan{},
	"group":             core.GroupInfo{},
	"php-check":         core.PHPCheckResult{},
	"ports":             core.PortsStatus{},
	"mcp-permissions":   core.MCPPermissions{},
	"mcp-actions":       []store.MCPAction{},
	"blueprints":        []core.BlueprintInfo{},
	"blueprint-result":  core.BlueprintResult{},
	"remotes":           []store.Remote{},
	"sync-result":       core.SyncResult{},
	"snapshots":         []store.Snapshot{},
	"restore-result":    core.RestoreResult{},
	"agent-sessions":    []core.SessionInfo{},
	"close-session":     core.CloseResult{},
	"session-diff":      core.SessionDiff{},
	"agent-tools":       core.AgentTools{},
	"agent-connect":     agents.ConnectResult{},
	"agent-start":       core.StartAgentResult{},
	"phone":             core.PhoneStatus{},
}

// TestWireGolden pins the JSON of every answer the app decodes, with every
// field filled in (omitempty fields included).
func TestWireGolden(t *testing.T) {
	if *update {
		if err := os.MkdirAll(wireDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, zero := range wireTypes {
		t.Run(name, func(t *testing.T) {
			v := reflect.New(reflect.TypeOf(zero)).Elem()
			fill(v, 0)
			var b bytes.Buffer
			if err := envelope.Write(&b, envelope.OK(v.Interface())); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(wireDir, name+".json")
			if *update {
				if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s (run go test ./internal/api -run TestWireGolden -update): %v", path, err)
			}
			if !bytes.Equal(b.Bytes(), want) {
				t.Errorf("%s changed: the app decodes this JSON. If the change is additive, rerun with -update and check the Swift model; renaming or removing a field breaks the app.\n--- got\n%s", path, b.String())
			}
		})
	}
	// No stale files: each golden file has its type above.
	files, _ := filepath.Glob(filepath.Join(wireDir, "*.json"))
	for _, f := range files {
		name := filepath.Base(f)
		if _, ok := wireTypes[name[:len(name)-len(".json")]]; !ok {
			t.Errorf("%s has no type in wireTypes", f)
		}
	}
}

var wireTime = time.Date(2026, 11, 4, 10, 30, 0, 0, time.UTC)

// fill sets every field to a non-zero value: strings "x", numbers 1, true,
// one element per slice and map, a fixed time. Nested values stop filling
// slices after a few levels (group trees).
func fill(v reflect.Value, depth int) {
	switch {
	case v.Type() == reflect.TypeOf(time.Time{}):
		v.Set(reflect.ValueOf(wireTime))
		return
	case v.Type() == reflect.TypeOf(json.RawMessage{}):
		v.Set(reflect.ValueOf(json.RawMessage(`{}`)))
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Pointer:
		if depth > 4 {
			return
		}
		p := reflect.New(v.Type().Elem())
		fill(p.Elem(), depth+1)
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 0, 1)
		if depth <= 4 {
			e := reflect.New(v.Type().Elem()).Elem()
			fill(e, depth+1)
			s = reflect.Append(s, e)
		}
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		if depth <= 4 {
			k := reflect.New(v.Type().Key()).Elem()
			fill(k, depth+1)
			e := reflect.New(v.Type().Elem()).Elem()
			fill(e, depth+1)
			m.SetMapIndex(k, e)
		}
		v.Set(m)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				fill(f, depth+1)
			}
		}
	case reflect.Interface:
		v.Set(reflect.ValueOf("x"))
	}
}
