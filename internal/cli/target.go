package cli

import (
	"github.com/epinio/epinio/internal/cli/clients"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var ()

// CmdTarget implements the epinio target command
var CmdTarget = &cobra.Command{
	Use:   "target [org]",
	Short: "Targets an organization in Epinio.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		client, err := clients.NewEpinioClient(cmd.Context())
		if err != nil {
			return errors.Wrap(err, "error initializing cli")
		}

		org := ""
		if len(args) > 0 {
			org = args[0]
		}

		err = client.Target(org)
		if err != nil {
			return errors.Wrap(err, "failed to set target")
		}

		return nil
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		app, err := clients.NewEpinioClient(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		matches := app.OrgsMatching(toComplete)

		return matches, cobra.ShellCompDirectiveNoFileComp
	},
}
