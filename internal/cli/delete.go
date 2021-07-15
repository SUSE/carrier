package cli

import (
	"github.com/epinio/epinio/internal/cli/clients"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var ()

// CmdDeleteApp implements the epinio delete command
var CmdDeleteApp = &cobra.Command{
	Use:   "delete NAME",
	Short: "Deletes an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		client, err := clients.NewEpinioClient(cmd.Context())
		if err != nil {
			return errors.Wrap(err, "error initializing cli")
		}

		err = client.Delete(cmd.Context(), args[0])
		if err != nil {
			return errors.Wrap(err, "error deleting app")
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

		matches := app.AppsMatching(cmd.Context(), toComplete)

		return matches, cobra.ShellCompDirectiveNoFileComp
	},
}
