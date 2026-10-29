package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/getcovo/covo/internal/core"
	"github.com/spf13/cobra"
)

type trustResult core.TrustResult

func (r trustResult) Text(w io.Writer) {
	switch {
	case r.Trusted && r.Changed:
		fmt.Fprintln(w, "Covo Local CA is now trusted: https://*.localhost sites open without warnings.")
	case r.Trusted:
		fmt.Fprintln(w, "Covo Local CA is already trusted.")
	case r.Changed:
		for _, c := range r.Removed {
			fmt.Fprintf(w, "Removed %s (SHA-1 %s) from the login keychain.\n", c.CommonName, c.SHA1)
		}
	default:
		fmt.Fprintln(w, "Covo Local CA was not in the login keychain.")
	}
}

func (a *app) trustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trust",
		Short: "Trust Covo's local certificate authority (asks macOS once)",
		Long: `Adds Covo's local root certificate to your login keychain as trusted for
your user only. macOS shows one dialog asking for your password or Touch ID.
No sudo, nothing system-wide. Undo with: covo untrust`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Trust(ctx, func() {
				fmt.Fprintln(a.stderr, "macOS will now ask for your password or Touch ID to trust \"Covo Local CA\" for your user.")
			})
			return trustResult(res), err
		}),
	}
}

func (a *app) untrustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "untrust",
		Short: "Remove Covo's certificate authority from the login keychain",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Untrust(ctx)
			return trustResult(res), err
		}),
	}
}
