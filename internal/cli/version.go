package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/getcovo/covo/internal/version"
	"github.com/spf13/cobra"
)

type versionResult version.Info

func (v versionResult) Text(w io.Writer) {
	fmt.Fprintf(w, "covo %s", v.Version)
	if v.Commit != "" {
		fmt.Fprintf(w, " (%.12s", v.Commit)
		if v.Dirty {
			fmt.Fprint(w, ", dirty")
		}
		fmt.Fprint(w, ")")
	}
	fmt.Fprintln(w)
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Covo version",
		Args:  cobra.NoArgs,
		RunE: a.run(func(context.Context, []string) (Result, error) {
			return versionResult(version.Get()), nil
		}),
	}
}
