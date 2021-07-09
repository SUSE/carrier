package cli

import (
	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/epinio/epinio/helpers/randstr"
	"github.com/epinio/epinio/internal/cli/clients"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var NeededOptions = kubernetes.InstallationOptions{
	{
		Name:        "skip-traefik",
		Description: "Assert to epinio that there is a Traefik active, even if epinio cannot find it.",
		Type:        kubernetes.BooleanType,
		Default:     false,
		Value:       false,
	},
	{
		Name:        "skip-linkerd",
		Description: "Assert to epinio that Linkerd is already installed.",
		Type:        kubernetes.BooleanType,
		Default:     false,
		Value:       false,
	},
	{
		Name:        "system_domain",
		Description: "The domain you are planning to use for Epinio. Should be pointing to the traefik public IP (Leave empty to use a omg.howdoi.website domain).",
		Type:        kubernetes.StringType,
		Default:     "",
		Value:       "",
	},
	{
		Name:        "email_address",
		Description: "The email address you are planning to use for getting notifications about your certificates",
		Type:        kubernetes.StringType,
		Default:     "epinio@suse.com",
		Value:       "",
	},
	{
		Name:        "user",
		Description: "The user name for authenticating all API requests",
		Type:        kubernetes.StringType,
		Default:     "",
		Value:       "",
		DynDefaultFunc: func(o *kubernetes.InstallationOption) error {
			uid, err := randstr.Hex16()
			if err != nil {
				return err
			}
			o.Value = uid
			return nil
		},
	},
	{
		Name:        "password",
		Description: "The password for authenticating all API requests",
		Type:        kubernetes.StringType,
		Default:     "",
		Value:       "",
		DynDefaultFunc: func(o *kubernetes.InstallationOption) error {
			uid, err := randstr.Hex16()
			if err != nil {
				return err
			}
			o.Value = uid
			return nil
		},
	},
}

var TraefikOptions = kubernetes.InstallationOptions{
	{
		Name:        "skip-linkerd",
		Description: "Assert to epinio that Linkerd is already installed.",
		Type:        kubernetes.BooleanType,
		Default:     false,
		Value:       false,
	},
}

const (
	DefaultOrganization = "workspace"
)

var CmdInstall = &cobra.Command{
	Use:   "install",
	Short: "install Epinio in your configured kubernetes cluster",
	Long:  `install Epinio PaaS in your configured kubernetes cluster`,
	Args:  cobra.ExactArgs(0),
	RunE:  Install,
}

// Note: The command is called `install-ingress` instead of `install
// ingress` because we already have `install` as a command, and Cobra
// does not allow such overlapped definitions. While we could convert
// the existing `install` command into an ensemble and then make the
// existing functionality available as `install <something>` it makes
// the quick install more verbose. So, for now the new functionality
// is exposed through a new toplevel command.

var CmdInstallIngress = &cobra.Command{
	Use:   "install-ingress",
	Short: "install Epinio's Ingress in your configured kubernetes cluster",
	Long:  `install Epinio Ingress Controller in your configured kubernetes cluster`,
	Args:  cobra.ExactArgs(0),
	RunE:  InstallIngress,
}

func init() {
	CmdInstall.Flags().BoolP("interactive", "i", false, "Whether to ask the user or not (default not)")
	CmdInstall.Flags().BoolP("skip-default-org", "s", false, "Set this to skip creating a default org")
	CmdInstall.Flags().StringP("ingress-ip", "l", "", "ip address to be assgined to ingress loadbalancer service")

	CmdInstallIngress.Flags().BoolP("interactive", "i", false, "Whether to ask the user or not (default not)")

	NeededOptions.AsCobraFlagsFor(CmdInstall)
	TraefikOptions.AsCobraFlagsFor(CmdInstallIngress)
}

// Install command installs epinio on a configured cluster
func Install(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true

	skipTraefik, err := cmd.Flags().GetBool("skip-traefik")
	if err != nil {
		return errors.Wrap(err, "could not read option --skip-traefik")
	}

	ingressIP, err := cmd.Flags().GetString("ingress-ip")
	if err != nil {
		return errors.Wrap(err, "could not read option --ingress-ip")
	}

	if ingressIP != "" && !skipTraefik {
		return errors.New("cannot have --skip-traeifk and --ingress-ip together")
	}

	installClient, installCleanup, err := clients.NewInstallClient(cmd.Context(), cmd.Flags(), &NeededOptions)
	defer func() {
		if installCleanup != nil {
			installCleanup()
		}
	}()

	if err != nil {
		return errors.Wrap(err, "error initializing cli")
	}

	err = installClient.Install(cmd)
	if err != nil {
		return errors.Wrap(err, "error installing Epinio")
	}

	// Installation complete. Run `org create`, and `target`.

	epinio_client, err := clients.NewEpinioClient(cmd.Context(), cmd.Flags())
	if err != nil {
		return errors.Wrap(err, "error initializing cli")
	}

	// Post Installation Tasks:
	// - Retrieve API certs and credentials, save to configuration
	//
	// - Create and target a default organization, so that the
	//   user can immediately begin to push applications.
	//
	// Dev Note: The targeting is done to ensure that a epinio
	// config left over from a previous installation will contain
	// a valid organization. Without it may contain the name of a
	// now invalid organization from said previous install. This
	// then breaks push and other commands in non-obvious ways.

	err = epinio_client.ConfigUpdate(cmd.Context())
	if err != nil {
		return errors.Wrap(err, "error updating config")
	}

	skipDefaultOrg, err := cmd.Flags().GetBool("skip-default-org")
	if err != nil {
		return errors.Wrap(err, "error reading option --skip-default-org")
	}

	if !skipDefaultOrg {
		err := epinio_client.CreateOrg(DefaultOrganization)

		if err != nil {
			return errors.Wrap(err, "error creating org")
		}

		err = epinio_client.Target(DefaultOrganization)
		if err != nil {
			return errors.Wrap(err, "failed to set target")
		}
	}

	return nil
}

// InstallIngress command installs epinio's ingress controller on a configured cluster
func InstallIngress(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true

	installClient, installCleanup, err := clients.NewInstallClient(cmd.Context(), cmd.Flags(), &TraefikOptions)
	defer func() {
		if installCleanup != nil {
			installCleanup()
		}
	}()

	if err != nil {
		return errors.Wrap(err, "error initializing cli")
	}

	err = installClient.InstallIngress(cmd)
	if err != nil {
		return errors.Wrap(err, "error installing Epinio")
	}

	return nil
}
