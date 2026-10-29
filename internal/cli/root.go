// Package cli implements the covo command line interface. It is a thin client:
// commands parse flags, call the core and render the result.
package cli

import (
	"context"
	"io"
	"os"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/spf13/cobra"
)

type app struct {
	stdout io.Writer
	stderr io.Writer
	json   bool
	// ran is set once a command's RunE starts; errors before that are usage errors.
	ran bool
}

// Execute runs the CLI with args and returns the process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	a := &app{stdout: stdout, stderr: stderr}
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		if !a.ran && errcode.Code(err) == errcode.Internal {
			err = errcode.New(errcode.InvalidInput, err)
			// Cobra stops before parsing flags on some usage errors (e.g. an
			// unknown command), so look for --json by hand.
			a.json = a.json || wantsJSON(args)
		}
		// In --json mode errors go to stdout so callers get one parseable document.
		if a.json {
			writeError(stdout, true, err)
		} else {
			writeError(stderr, false, err)
		}
		return 1
	}
	return 0
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "covo",
		Short:         "Local PHP / WordPress development environment",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&a.json, "json", false, "output JSON with a stable schema")
	root.AddCommand(a.versionCmd())
	return root
}

func wantsJSON(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "--json", "--json=true":
			return true
		}
	}
	return false
}

// run adapts a command function returning a Result to cobra.
func (a *app) run(fn func(ctx context.Context, args []string) (Result, error)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		a.ran = true
		r, err := fn(cmd.Context(), args)
		if err != nil {
			return err
		}
		return writeResult(a.stdout, a.json, r)
	}
}

// Main is the entrypoint used by cmd/covo.
func Main() {
	os.Exit(Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
