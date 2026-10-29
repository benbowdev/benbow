// Package cli implements the covo command line interface. It is a thin client:
// commands parse flags, call the core and render the result.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/spf13/cobra"
)

type app struct {
	// stdin is nil when the CLI must not prompt (tests, pipes).
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	json   bool
	// ran is set once a command's RunE starts; errors before that are usage errors.
	ran bool
}

// Execute runs the CLI with args and returns the process exit code. It never
// prompts for confirmation.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return execute(ctx, args, nil, stdout, stderr)
}

func execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
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
	root.AddCommand(a.versionCmd(), a.startCmd(), a.stopCmd(), a.statusCmd(), a.siteCmd(), a.phpCmd(), a.caddyCmd())
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

// withCore is run for commands that need the core.
func (a *app) withCore(fn func(ctx context.Context, c *core.Core, args []string) (Result, error)) func(*cobra.Command, []string) error {
	return a.run(func(ctx context.Context, args []string) (Result, error) {
		c, err := core.Open(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = c.Close() }()
		return fn(ctx, c, args)
	})
}

// Main is the entrypoint used by cmd/covo.
func Main() {
	os.Exit(execute(context.Background(), os.Args[1:], terminalStdin(), os.Stdout, os.Stderr))
}

// terminalStdin returns os.Stdin if it is a terminal, nil otherwise.
func terminalStdin() io.Reader {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	return os.Stdin
}

// confirm asks a yes/no question. It returns false without asking in --json
// mode or when stdin is not a terminal: callers must then pass --force.
func (a *app) confirm(question string) bool {
	if a.json || a.stdin == nil {
		return false
	}
	fmt.Fprintf(a.stderr, "%s [y/N] ", question)
	line, _ := bufio.NewReader(a.stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
