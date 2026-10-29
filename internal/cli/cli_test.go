package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestJSONEnvelope(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOK   bool
		wantErr  string
	}{
		{"version ok", []string{"version", "--json"}, 0, true, ""},
		{"unknown command", []string{"nope", "--json"}, 1, false, "invalid_input"},
		{"bad args", []string{"version", "extra", "--json"}, 1, false, "invalid_input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Execute(context.Background(), tt.args, &out, &errOut)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr %q)", code, tt.wantCode, errOut.String())
			}
			var env struct {
				Schema int             `json:"schema"`
				OK     bool            `json:"ok"`
				Data   json.RawMessage `json:"data"`
				Error  *ErrorBody      `json:"error"`
			}
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
			}
			if env.Schema != SchemaVersion || env.OK != tt.wantOK {
				t.Fatalf("envelope = %+v", env)
			}
			if tt.wantErr != "" && (env.Error == nil || env.Error.Code != tt.wantErr) {
				t.Fatalf("error = %+v, want code %q", env.Error, tt.wantErr)
			}
			if tt.wantOK && len(env.Data) == 0 {
				t.Fatal("missing data")
			}
		})
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		name  string
		stdin io.Reader
		json  bool
		want  bool
	}{
		{"yes", strings.NewReader("y\n"), false, true},
		{"YES", strings.NewReader("YES\n"), false, true},
		{"no", strings.NewReader("n\n"), false, false},
		{"empty", strings.NewReader("\n"), false, false},
		{"no terminal", nil, false, false},
		{"json never prompts", strings.NewReader("y\n"), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errOut bytes.Buffer
			a := &app{stdin: tt.stdin, stderr: &errOut, json: tt.json}
			if got := a.confirm("ok?"); got != tt.want {
				t.Fatalf("confirm = %v, want %v", got, tt.want)
			}
			if (tt.stdin == nil || tt.json) && errOut.Len() != 0 {
				t.Fatalf("prompted without a terminal: %q", errOut.String())
			}
		})
	}
}
