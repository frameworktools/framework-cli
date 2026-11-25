package cli

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

func newArtifactsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifacts",
		Short: "Manage published artifacts on Framework Cloud",
	}
	cmd.AddCommand(newArtifactsFlattenCmd(a))
	return cmd
}

func newArtifactsFlattenCmd(a *app) *cobra.Command {
	var urlFlag, project string
	var yes bool
	cmd := &cobra.Command{
		Use:   "flatten SLUG",
		Short: "Keep only an artifact's newest version, as version 1",
		Long: "Makes an artifact's newest version its only one, numbered 1, and deletes\n" +
			"the older versions and their files for good. The Trash doesn't keep them.\n" +
			"Links and embeds of old versions open the newest one. Collections and\n" +
			"reports pinned to an old version move to version 1. The next publish\n" +
			"continues after the highest old number. External publications can't be\n" +
			"flattened.\n\n" +
			"Inside a project linked to Framework Cloud, fw uses the project token in\n" +
			".env, as fw publish does, and flattens the project's artifact. With\n" +
			"--project, or outside a linked project, it uses your login; --project\n" +
			"names the project when several of yours have an artifact with this slug.\n\n" +
			"fw asks first, defaulting to No; --yes skips the question. Without a\n" +
			"terminal, --yes is required.",
		Example: "  fw artifacts flatten treatment-effect\n" +
			"  fw artifacts flatten treatment-effect --project vaccine-study --yes --json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			client, ownProject, explainErr, err := a.artifactClient(urlFlag, project)
			if err != nil {
				return err
			}
			where := "on " + cloud.Host(client.URL)
			switch {
			case project != "":
				where = "in project " + project + " " + where
			case ownProject:
				where = "in this project " + where
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Flattening %s %s keeps its newest version as version 1 and deletes the older versions for good.\n", slug, where)
			if !yes {
				if !a.interactive {
					return usageError("no terminal to confirm from. After reading the line above, rerun with --yes")
				}
				ok, err := a.askYes(cmd, "Flatten it?")
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("canceled; nothing was changed")
				}
			}
			res, err := client.Flatten(cmd.Context(), slug, project)
			switch cloud.StatusOf(err) {
			case http.StatusNotFound:
				return &exitError{code: exitFailure, reason: "not_found", err: fmt.Errorf("%w. No artifact %s was found %s", err, slug, where)}
			case http.StatusConflict:
				return &exitError{code: exitFailure, reason: "ambiguous_slug", err: fmt.Errorf("%w. Name the project with --project", err)}
			}
			if err != nil {
				return explainErr(err)
			}
			out := cmd.OutOrStdout()
			if a.jsonOut {
				return writeJSON(out, res)
			}
			name := res.Artifact
			if name == "" {
				name = slug
			}
			fmt.Fprintf(out, "Flattened %s to version %d; removed %d older %s.\n", name, res.Version, res.Removed, plural(res.Removed, "version", "versions"))
			if res.ArtifactURL != "" {
				fmt.Fprintf(out, "Manage: %s\n", res.ArtifactURL)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "the artifact's project slug (uses your login instead of the project token)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "flatten without asking")
	cmd.Flags().StringVar(&urlFlag, "url", "", "Framework Cloud server (default: the project's FW_CLOUD_URL, then your login's)")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// artifactClient picks the token for an artifact command: the project token
// inside a linked project, as fw publish uses, unless --project names a
// project; otherwise the user's login.
func (a *app) artifactClient(urlFlag, project string) (client *cloud.Client, ownProject bool, explainErr func(error) error, err error) {
	if root := a.projectRoot(); project == "" && root != "" && a.projectToken(root) != "" {
		client, _, err := a.projectClient(urlFlag)
		if err != nil {
			return nil, false, nil, err
		}
		return client, true, explainProject, nil
	}
	client, err = a.userClient(urlFlag)
	if err != nil {
		return nil, false, nil, err
	}
	return client, false, func(err error) error { return explain(err, client.URL) }, nil
}
