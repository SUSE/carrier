package cli

import (
	"github.com/epinio/epinio/internal/cli/clients"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var ()

// CmdInfo implements the epinio info command
var CmdInfo = &cobra.Command{
	Use:   "info",
	Short: "Shows information about the Epinio environment",
	Long:  `Shows status and version for Kubernetes, Gitea, Tekton and Eirini.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		client, err := clients.NewEpinioClient(cmd.Context())
		if err != nil {
			return errors.Wrap(err, "error initializing cli")
		}

		err = client.Info()
		if err != nil {
			return errors.Wrap(err, "error retrieving Epinio environment information")
		}

		return nil
	},
}
