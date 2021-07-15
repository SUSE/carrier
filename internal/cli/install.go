package cli

import (
	"fmt"

	"github.com/epinio/epinio/deployments"
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
	{
		Name: "tls-issuer",
		Description: fmt.Sprintf("The name of the cluster issuer to use. Epinio creates three options: '%s', '%s', and '%s'.",
			deployments.EpinioCAIssuer,
			deployments.LetsencryptIssuer,
			deployments.SelfSignedIssuer),
		Type:    kubernetes.StringType,
		Default: deployments.EpinioCAIssuer,
		Value:   deployments.EpinioCAIssuer,
	},
	{
		Name:        "use-internal-registry-node-port",
		Description: "Make the internal registry accessible via a node port, so kubelet can access the registry without trusting its cert.",
		Type:        kubernetes.BooleanType,
		Default:     true,
		Value:       true,
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

	CmdInstallIngress.Flags().BoolP("interactive", "i", false, "Whether to ask the user or not (default not)")

	NeededOptions.AsCobraFlagsFor(CmdInstall.Flags())
	TraefikOptions.AsCobraFlagsFor(CmdInstallIngress.Flags())
}

// Install command installs epinio on a configured cluster
func Install(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true

	installClient, installCleanup, err := clients.NewInstallClient(cmd.Context(), &NeededOptions)
	defer func() {
		if installCleanup != nil {
			installCleanup()
		}
	}()

	if err != nil {
		return errors.Wrap(err, "error initializing cli")
	}

	err = installClient.Install(cmd.Context(), cmd.Flags())
	if err != nil {
		return errors.Wrap(err, "error installing Epinio")
	}

	// Installation complete. Run `org create`, and `target`.

	epinioClient, err := clients.NewEpinioClient(cmd.Context())
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

	err = epinioClient.ConfigUpdate(cmd.Context())
	if err != nil {
		return errors.Wrap(err, "error updating config")
	}

	skipDefaultOrg, err := cmd.Flags().GetBool("skip-default-org")
	if err != nil {
		return errors.Wrap(err, "error reading option --skip-default-org")
	}

	if !skipDefaultOrg {
		err := epinioClient.CreateOrg(DefaultOrganization)

		if err != nil {
			return errors.Wrap(err, "error creating org")
		}

		err = epinioClient.Target(DefaultOrganization)
		if err != nil {
			return errors.Wrap(err, "failed to set target")
		}
	}

	return nil
}

// InstallIngress command installs epinio's ingress controller on a configured cluster
func InstallIngress(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true

	installClient, installCleanup, err := clients.NewInstallClient(cmd.Context(), &TraefikOptions)
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
