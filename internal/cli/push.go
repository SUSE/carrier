package cli

import (
	"os"
	"strings"

	v1 "github.com/epinio/epinio/internal/api/v1"
	"github.com/epinio/epinio/internal/cli/clients"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var ()

func init() {
	CmdPush.Flags().Int32P("instances", "i", v1.DefaultInstances,
		"The number of desired instances for the application, default only applies to new deployments")
	CmdPush.Flags().String("git", "", "git revision of sources. PATH becomes repository location")
	CmdPush.Flags().String("docker", "", "docker repo for image")
	CmdPush.Flags().StringSliceP("bind", "b", []string{}, "services to bind immediately")
	CmdPush.RegisterFlagCompletionFunc("bind",
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			// `cmd`, `args` are ignored.
			// `toComplete` is the option value entered so far.
			// This is a StringSlice option.
			// This means that the option value is a comma-separated
			// string of values.
			// Completion has to happen only for the last segment in
			// that string, i.e. after the last comma.  Note that
			// cobra does not feed us a slice, just the string.  We
			// are responsible for splitting into segments, and
			// expanding only the last segment.

			ctx := cmd.Context()

			app, err := clients.NewEpinioClient(ctx, cmd.Flags())
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}

			values := strings.Split(toComplete, ",")
			if len(values) == 0 {
				// Nothing. Report all possible matches
				matches := app.ServiceMatching(ctx, toComplete)
				return matches, cobra.ShellCompDirectiveNoFileComp
			}

			// Expand the last segment. The returned matches are
			// the string with its last segment replaced by the
			// expansions for that segment.

			matches := []string{}
			for _, match := range app.ServiceMatching(ctx, values[len(values)-1]) {
				values[len(values)-1] = match
				matches = append(matches, strings.Join(values, ","))
			}

			return matches, cobra.ShellCompDirectiveDefault
		})
}

// instances checks if the user provided an instance count. If they didn't, then we'll
// pass nil and either use the default or whatever is deployed in the cluster.
func instances(cmd *cobra.Command) (*int32, error) {
	var i *int32
	instances, err := cmd.Flags().GetInt32("instances")
	if err != nil {
		cmd.SilenceUsage = false
		return i, errors.Wrap(err, "could not read instances parameter")
	}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Name == "instances" {
			n := int32(instances)
			i = &n
		}
	})
	return i, nil
}

// CmdPush implements the epinio push command
var CmdPush = &cobra.Command{
	Use:   "push NAME [URL|PATH_TO_APPLICATION_SOURCES]",
	Short: "Push an application from the specified directory, or the current working directory",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		client, err := clients.NewEpinioClient(cmd.Context(), cmd.Flags())
		if err != nil {
			return errors.Wrap(err, "error initializing cli")
		}

		gitRevision, err := cmd.Flags().GetString("git")
		if err != nil {
			return errors.Wrap(err, "could not read option --git")
		}

		dockerRepo, err := cmd.Flags().GetString("docker")
		if err != nil {
			return errors.Wrap(err, "could not read option --docker")
		}

		if gitRevision != "" && dockerRepo != "" {
			return errors.Wrap(err, "cannot use both, git and docker")
		}

		// Syntax:
		// 1. push NAME
		// 2. push NAME PATH
		// 3. push NAME URL --git REV
		// 3. push NAME --docker URL

		var path string
		if len(args) == 1 {
			if gitRevision != "" {
				// Missing argument is user error. Show usage
				cmd.SilenceUsage = false
				return errors.New("app name or git repository url missing")
			}

			path, err = os.Getwd()
			if err != nil {
				return errors.Wrap(err, "working directory not accessible")
			}
		} else {
			path = args[1]
		}

		if dockerRepo != "" {
			path = ""
		}

		if gitRevision == "" && dockerRepo == "" {
			if _, err := os.Stat(path); err != nil {
				// Path issue is user error. Show usage
				cmd.SilenceUsage = false
				return errors.Wrap(err, "path not accessible")
			}
		}

		i, err := instances(cmd)
		if err != nil {
			return errors.Wrap(err, "trouble with instances")
		}
		params := clients.PushParams{
			Instances: i,
			GitRev:    gitRevision,
			Docker:    dockerRepo,
		}

		services, err := cmd.Flags().GetStringSlice("bind")
		if err != nil {
			return errors.Wrap(err, "failed to read option --bind")
		}
		params.Services = services

		err = client.Push(cmd.Context(), args[0], path, params)
		if err != nil {
			return errors.Wrap(err, "error pushing app to server")
		}

		return nil
	},
}
