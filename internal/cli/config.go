package cli

import (
	"fmt"
	"strconv"

	"github.com/epinio/epinio/helpers/termui"
	"github.com/epinio/epinio/internal/cli/clients"
	"github.com/epinio/epinio/internal/cli/config"
	"github.com/fatih/color"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var ()

// CmdConfig implements the `epinio config` command
var CmdConfig = &cobra.Command{
	Use:           "config",
	Short:         "Epinio config management",
	Long:          `Manage the epinio cli configuration`,
	Args:          cobra.ExactArgs(0),
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	CmdConfig.AddCommand(CmdConfigUpdateCreds)
	CmdConfig.AddCommand(CmdConfigShow)
	CmdConfig.AddCommand(CmdConfigColors)
}

// CmdConfigColors implements the `epinio config colors` command
var CmdConfigColors = &cobra.Command{
	Use:   "colors BOOL",
	Short: "Manage colored output",
	Long:  "Enable/Disable colored output",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return errors.New("requires a boolean argument")
		}
		_, err := strconv.ParseBool(args[0])
		if err != nil {
			return errors.New("requires a boolean argument")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Hello, World!")
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		ui := termui.NewUI()

		theConfig, err := config.Load()
		if err != nil {
			return errors.Wrap(err, "failed to load configuration")
		}

		colors, err := strconv.ParseBool(args[0])
		// assert: err == nil -- see args validation
		if err != nil {
			return errors.Wrap(err, "unexpected bool parsing error")
		}

		theConfig.Colors = colors
		theConfig.Save()

		ui.Success().WithBoolValue("Colors", theConfig.Colors).Msg("Ok")
		return nil
	},
}

// CmdConfigShow implements the `epinio config show` command
var CmdConfigShow = &cobra.Command{
	Use:   "show",
	Short: "Show the current configuration",
	Args:  cobra.ExactArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		ui := termui.NewUI()

		theConfig, err := config.Load()
		if err != nil {
			return errors.Wrap(err, "failed to load configuration")
		}

		certInfo := color.CyanString("None defined")
		if theConfig.Certs != "" {
			certInfo = color.BlueString("Present")
		}

		ui.Success().
			WithTable("Key", "Value").
			WithTableRow("Colorized Output", color.MagentaString("%t", theConfig.Colors)).
			WithTableRow("Current Organization", color.CyanString(theConfig.Org)).
			WithTableRow("API User Name", color.BlueString(theConfig.User)).
			WithTableRow("API Password", color.BlueString(theConfig.Password)).
			WithTableRow("Certificates", certInfo).
			Msg("Ok")

		return nil
	},
}

// CmdConfigUpdateCreds implements the `epinio config update-credentials` command
var CmdConfigUpdateCreds = &cobra.Command{
	Use:   "update-credentials",
	Short: "Update the stored credentials",
	Long:  "Update the stored credentials from the current cluster",
	Args:  cobra.ExactArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		client, err := clients.NewEpinioClient(cmd.Context())

		if err != nil {
			return errors.Wrap(err, "error initializing cli")
		}

		err = client.ConfigUpdate(cmd.Context())
		if err != nil {
			return errors.Wrap(err, "failed to update the configuration")
		}

		return nil
	},
}
