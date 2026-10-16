package cmd

// `ankra org oidc-trust create|list|delete` (ankra-4pigj8): the organisation's
// GitHub Actions OIDC trust policies. A policy lets one GitHub repository
// exchange its workflow's OIDC ID token for a short-lived Ankra token
// (`ankra login --github-oidc`) with no stored secret.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var orgOIDCTrustCmd = &cobra.Command{
	Use:   "oidc-trust",
	Short: "Manage GitHub Actions OIDC trust policies (no stored CI token)",
	Long: `Manage the GitHub Actions OIDC trust policies of the organisation.

A policy names ONE repository exactly, by owner/name AND by the numeric
repository and owner ids (which GitHub never reuses, so a renamed or
re-created repository cannot inherit it), and optionally a ref, an
environment and a workflow. A job from that repository then runs

  ankra login --github-oidc --organisation-id <id>

and receives a token that lasts minutes, carries the policy's role, and is
never stored in a GitHub secret. Pull request runs are refused unless the
policy opts in with --allow-pull-request; pull_request_target never is.

Find the ids with:  gh api repos/OWNER/NAME --jq '.id, .owner.id'

Requires organisation admin.`,
}

var orgOIDCTrustListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the organisation's OIDC trust policies",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		policies, listError := apiClient.ListOIDCTrustPolicies(ctx)
		if listError != nil {
			return fmt.Errorf("list OIDC trust policies: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, policies); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		if len(policies) == 0 {
			_, _ = fmt.Fprintln(out, "No OIDC trust policies. Create one with: ankra org oidc-trust create --help")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleLight)
		writer.AppendHeader(table.Row{"Name", "Repository", "Ref", "Environment", "Role", "TTL", "PRs", "ID"})
		for _, policy := range policies {
			writer.AppendRow(table.Row{policy.Name, policy.RepositoryOwner + "/" + policy.RepositoryName,
				oidcValueOrDash(policy.Ref), oidcValueOrDash(policy.Environment), policy.RoleSlug,
				(time.Duration(policy.TTLSeconds) * time.Second).String(), policy.AllowPullRequest, policy.ID})
		}
		writer.Render()
		return nil
	},
}

func oidcValueOrDash(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

func optionalFlag(cmd *cobra.Command, name string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	value, _ := cmd.Flags().GetString(name)
	return &value
}

var orgOIDCTrustCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Trust one GitHub repository to exchange its OIDC token",
	Long: `Create a trust policy.

  ankra org oidc-trust create deploy-main \
    --repository acme/deploy --repository-id 123456 --repository-owner-id 7890 \
    --ref refs/heads/main --environment production --role operator

--repository, --repository-id, --repository-owner-id and --role are required.
No value accepts wildcards. --ttl is how long the minted token lives
(default 15m, at most 1h). Prefer pinning an --environment that requires
reviewers in GitHub.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repository, _ := cmd.Flags().GetString("repository")
		owner, name, found := strings.Cut(repository, "/")
		if !found || owner == "" || name == "" || strings.Contains(name, "/") {
			return withExitCode(exitUsage, errors.New("--repository must be OWNER/NAME"))
		}
		repositoryID, _ := cmd.Flags().GetInt64("repository-id")
		ownerID, _ := cmd.Flags().GetInt64("repository-owner-id")
		role, _ := cmd.Flags().GetString("role")
		ttl, _ := cmd.Flags().GetDuration("ttl")
		allowPR, _ := cmd.Flags().GetBool("allow-pull-request")
		scopes, _ := cmd.Flags().GetStringSlice("scope")
		request := client.OIDCTrustPolicyRequest{
			Name: args[0], RepositoryOwner: owner, RepositoryName: name,
			RepositoryOwnerID: ownerID, RepositoryID: repositoryID,
			Ref: optionalFlag(cmd, "ref"), Environment: optionalFlag(cmd, "environment"),
			JobWorkflowRef:   optionalFlag(cmd, "job-workflow-ref"),
			AllowPullRequest: allowPR, RoleSlug: role, Scopes: scopes,
			TTLSeconds: int(ttl / time.Second),
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		policy, createError := apiClient.CreateOIDCTrustPolicy(ctx, request)
		if createError != nil {
			return fmt.Errorf("create the OIDC trust policy: %w", createError)
		}
		if rendered, renderError := renderStructured(cmd, policy); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created trust policy %s (%s) for %s/%s.\n",
			policy.Name, policy.ID, policy.RepositoryOwner, policy.RepositoryName)
		return nil
	},
}

var orgOIDCTrustDeleteCmd = &cobra.Command{
	Use:     "delete <policy-id>",
	Aliases: []string{"rm"},
	Short:   "Delete a trust policy and revoke the tokens it minted",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		skip, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Delete trust policy %s and revoke its live tokens? [y/N] ", args[0]), skip); confirmError != nil {
			return confirmError
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		if deleteError := apiClient.DeleteOIDCTrustPolicy(ctx, args[0]); deleteError != nil {
			return fmt.Errorf("delete the OIDC trust policy: %w", deleteError)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Deleted.")
		return nil
	},
}

func init() {
	create := orgOIDCTrustCreateCmd.Flags()
	create.String("repository", "", "GitHub repository as OWNER/NAME (exact)")
	create.Int64("repository-id", 0, "numeric GitHub repository id (gh api repos/OWNER/NAME --jq .id)")
	create.Int64("repository-owner-id", 0, "numeric GitHub owner id (gh api repos/OWNER/NAME --jq .owner.id)")
	create.String("ref", "", "exact ref the job must run on, e.g. refs/heads/main")
	create.String("environment", "", "exact GitHub environment the job must use")
	create.String("job-workflow-ref", "", "exact workflow ref, e.g. OWNER/NAME/.github/workflows/ci.yml@refs/heads/main")
	create.Bool("allow-pull-request", false, "also trust pull_request runs (never fork-safe; default off)")
	create.String("role", "", "role the minted token carries (e.g. operator)")
	create.StringSlice("scope", nil, "optional token scope restriction (repeatable)")
	create.Duration("ttl", 15*time.Minute, "lifetime of the minted token (1m to 1h)")
	_ = orgOIDCTrustCreateCmd.MarkFlagRequired("repository")
	_ = orgOIDCTrustCreateCmd.MarkFlagRequired("repository-id")
	_ = orgOIDCTrustCreateCmd.MarkFlagRequired("repository-owner-id")
	_ = orgOIDCTrustCreateCmd.MarkFlagRequired("role")
	orgOIDCTrustDeleteCmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	orgOIDCTrustCmd.AddCommand(orgOIDCTrustListCmd, orgOIDCTrustCreateCmd, orgOIDCTrustDeleteCmd)
	orgCmd.AddCommand(orgOIDCTrustCmd)
}
