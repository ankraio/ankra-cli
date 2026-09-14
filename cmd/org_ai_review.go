package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var orgAIReviewCmd = &cobra.Command{
	Use:   "ai-review",
	Short: "Configure what the organisation's AI code review may read",
	Long: `Configure the organisation's AI code review.

Switching review on for a connection stays in the portal's Source control & AI
review settings, and the model a review runs on is chosen with
'ankra ai lanes set pr_review <model>'.`,
}

var orgAIReviewRelatedReposCmd = &cobra.Command{
	Use:     "related-repos",
	Aliases: []string{"related-repositories"},
	Short:   "State which repositories an AI code review may also read",
	Long: `State which repositories an AI code review may also read.

When a pull request removes something other code may depend on, the review
searches the repositories related to the one under review for code that still
uses it. Applications Ankra installs on the same cluster are related with no
configuration. These commands state the relationships a deploy graph cannot
know about: a client and the API it calls, or a shared library.

  ankra org ai-review related-repos list
  ankra org ai-review related-repos add my-org/portal my-org/api
  ankra org ai-review related-repos remove my-org/portal my-org/api

A relationship has no direction: reviews of either repository consider the
other, so there is no mirror to add.

Relationships belong to one GitHub App installation, and both repositories must
be ones that installation can reach. Ankra reads them with the installation's
own token, so a pair naming a repository outside it is refused rather than
stored. Pick the installation with --credential, a GitHub credential name or ID
from 'ankra credentials list --provider github'. It can be left out when the
organisation has exactly one GitHub App credential.

Listing requires organisation membership; adding and removing require
organisation admin.`,
}

var orgAIReviewRelatedReposListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the related repositories stated under a GitHub App installation",
	Example: `  ankra org ai-review related-repos list
  ankra org ai-review related-repos list --credential github-app-my-org -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		requestedCredential, _ := cmd.Flags().GetString("credential")
		installation, resolveError := resolveRelatedReposInstallation(requestedCredential)
		if resolveError != nil {
			return resolveError
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), relatedReposRequestTimeout)
		defer cancel()

		related, listError := apiClient.ListRelatedRepositories(ctx, installation.installationID)
		if listError != nil {
			return fmt.Errorf("listing related repositories: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, related); rendered || renderError != nil {
			return renderError
		}
		renderRelatedRepositories(cmd.OutOrStdout(), installation, related)
		return nil
	},
}

var orgAIReviewRelatedReposAddCmd = &cobra.Command{
	Use:   "add <repository> <related-repository>",
	Short: "Relate two repositories for AI code review",
	Long: `Relate two repositories, so an AI code review of either may read the other.

Both are the provider's repository path, owner/name. Ankra checks with GitHub
that the chosen installation reaches both before it stores the pair, and
refuses a pair it could never read.

Requires organisation admin.`,
	Example: `  ankra org ai-review related-repos add my-org/portal my-org/api
  ankra org ai-review related-repos add my-org/web my-org/shared-lib --credential github-app-my-org`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		requestedCredential, _ := cmd.Flags().GetString("credential")
		installation, resolveError := resolveRelatedReposInstallation(requestedCredential)
		if resolveError != nil {
			return resolveError
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), relatedReposRequestTimeout)
		defer cancel()

		created, createError := apiClient.CreateRelatedRepository(ctx, installation.installationID, args[0], args[1])
		if createError != nil {
			return fmt.Errorf("relating %s and %s: %w", args[0], args[1], createError)
		}
		// An answer without the stored row confirms nothing, so it is not
		// reported as a relationship that now exists.
		if created == nil || created.ID == "" {
			return fmt.Errorf("the platform accepted relating %s and %s but its answer did not include the "+
				"relationship, so whether it was stored is unconfirmed; run "+
				"'ankra org ai-review related-repos list' to check", args[0], args[1])
		}
		if rendered, renderError := renderStructured(cmd, created); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Related %s ↔ %s under %s. A review of either repository can now read the other.\n",
			created.RepoFullName, created.RelatedRepoFullName, installation.describe())
		return nil
	},
}

var orgAIReviewRelatedReposRemoveCmd = &cobra.Command{
	Use:     "remove (<id> | <repository> <related-repository>)",
	Aliases: []string{"rm", "delete"},
	Short:   "Stop an AI code review consulting a related repository",
	Long: `Stop an AI code review consulting a related repository.

Name the relationship by its ID from 'list', or by its two repositories in
either order. A relationship has no direction, so naming the repositories
removes every stored row relating them, whichever order each was stated in.
Removing a relationship changes nothing about reviews already posted.

--credential is only read when the repositories are named, to find the
installation the relationship is stated under.

Requires organisation admin.`,
	Example: `  ankra org ai-review related-repos remove my-org/portal my-org/api
  ankra org ai-review related-repos remove 0b6f6d1e-6c1d-4f3a-9b43-7f6c2b1e9d10 --yes`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		skipConfirmation, _ := cmd.Flags().GetBool("yes")

		ctx, cancel := context.WithTimeout(cmd.Context(), relatedReposRequestTimeout)
		defer cancel()

		if len(args) == 1 {
			relationshipID := args[0]
			if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
				fmt.Sprintf("Remove related repository relationship %s? [y/N]: ", relationshipID),
				skipConfirmation); confirmError != nil {
				return confirmError
			}
			if deleteError := apiClient.DeleteRelatedRepository(ctx, relationshipID); deleteError != nil {
				return fmt.Errorf("removing related repository relationship %s: %w", relationshipID, deleteError)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed related repository relationship %s.\n", relationshipID)
			return nil
		}

		repository, relatedRepository := args[0], args[1]
		requestedCredential, _ := cmd.Flags().GetString("credential")
		installation, resolveError := resolveRelatedReposInstallation(requestedCredential)
		if resolveError != nil {
			return resolveError
		}
		related, listError := apiClient.ListRelatedRepositories(ctx, installation.installationID)
		if listError != nil {
			return fmt.Errorf("listing related repositories to find %s ↔ %s: %w", repository, relatedRepository, listError)
		}
		matches := relationshipsBetween(related, repository, relatedRepository)
		if len(matches) == 0 {
			return withExitCode(exitNotFound, fmt.Errorf(
				"no relationship between %s and %s is stated under %s; run "+
					"'ankra org ai-review related-repos list' to see the ones that are",
				repository, relatedRepository, installation.describe()))
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Remove the relationship between %s and %s? [y/N]: ", repository, relatedRepository),
			skipConfirmation); confirmError != nil {
			return confirmError
		}
		for matchIndex, match := range matches {
			if deleteError := apiClient.DeleteRelatedRepository(ctx, match.ID); deleteError != nil {
				if matchIndex > 0 {
					return fmt.Errorf("removed %d of the %d stored rows relating %s and %s, then removing %s failed, "+
						"so the relationship is still stated: %w",
						matchIndex, len(matches), repository, relatedRepository, match.ID, deleteError)
				}
				return fmt.Errorf("removing the relationship between %s and %s: %w",
					repository, relatedRepository, deleteError)
			}
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed the relationship between %s and %s under %s.\n",
			repository, relatedRepository, installation.describe())
		return nil
	},
}

// relatedReposRequestTimeout bounds each related-repositories command. Adding
// a pair makes the platform page through the installation's repositories on
// GitHub before it answers, so this matches the minute the other organisation
// settings commands allow.
const relatedReposRequestTimeout = 60 * time.Second

// relatedReposInstallation is the GitHub App installation relationships are
// stated under, kept with the credential that named it so output can say
// which installation a listing or a write applied to.
type relatedReposInstallation struct {
	credentialName string
	installationID string
}

func (installation relatedReposInstallation) describe() string {
	return fmt.Sprintf("%s (installation %s)", installation.credentialName, installation.installationID)
}

// resolveRelatedReposInstallation finds the installation a command acts on
// from the organisation's GitHub credentials. A failed credential listing is
// an error, never an organisation without an App credential.
func resolveRelatedReposInstallation(requestedCredential string) (relatedReposInstallation, error) {
	provider := "github"
	credentials, listError := apiClient.ListCredentials(&provider)
	if listError != nil {
		return relatedReposInstallation{}, fmt.Errorf("listing GitHub credentials to find the installation: %w", listError)
	}
	credential, selectError := selectRelatedReposCredential(credentials, strings.TrimSpace(requestedCredential))
	if selectError != nil {
		return relatedReposInstallation{}, selectError
	}
	return relatedReposInstallation{
		credentialName: credential.Name,
		installationID: strconv.Itoa(*credential.InstallationID),
	}, nil
}

// selectRelatedReposCredential picks the GitHub credential whose installation
// relationships are stated under. Only a credential backed by a GitHub App
// installation qualifies: relationships are scoped to an installation and read
// with its token, and a token-backed GitHub credential has neither. With no
// credential named, the organisation's only App credential is used; with
// several, the command refuses rather than guessing, because a pair stated
// under the wrong installation is refused there, or listed as absent.
func selectRelatedReposCredential(credentials []client.Credential, requestedCredential string) (client.Credential, error) {
	githubCredentials := make([]client.Credential, 0, len(credentials))
	appCredentials := make([]client.Credential, 0, len(credentials))
	for _, credential := range credentials {
		if !strings.EqualFold(credential.Provider, "github") {
			continue
		}
		githubCredentials = append(githubCredentials, credential)
		if credential.InstallationID != nil {
			appCredentials = append(appCredentials, credential)
		}
	}

	if requestedCredential != "" {
		selected, found := client.Credential{}, false
		for _, credential := range githubCredentials {
			if credential.ID == requestedCredential {
				selected, found = credential, true
				break
			}
		}
		if !found {
			nameMatches := make([]client.Credential, 0, 1)
			for _, credential := range githubCredentials {
				if credential.Name == requestedCredential {
					nameMatches = append(nameMatches, credential)
				}
			}
			switch len(nameMatches) {
			case 0:
				return client.Credential{}, withExitCode(exitNotFound, fmt.Errorf(
					"GitHub credential %q was not found; run 'ankra credentials list --provider github'",
					requestedCredential))
			case 1:
				selected = nameMatches[0]
			default:
				return client.Credential{}, withExitCode(exitUsage, fmt.Errorf(
					"multiple GitHub credentials are named %q; pass the credential ID instead", requestedCredential))
			}
		}
		if selected.InstallationID == nil {
			return client.Credential{}, withExitCode(exitUsage, fmt.Errorf(
				"GitHub credential %q is not backed by a GitHub App installation, and related repositories are "+
					"stated per installation; pass a GitHub App credential", requestedCredential))
		}
		return selected, nil
	}

	switch len(appCredentials) {
	case 1:
		return appCredentials[0], nil
	case 0:
		return client.Credential{}, errors.New(
			"no GitHub credential in this organisation reports a GitHub App installation, and related " +
				"repositories are stated per installation; install the Ankra GitHub App, then run this command again")
	default:
		names := make([]string, 0, len(appCredentials))
		for _, credential := range appCredentials {
			names = append(names, credential.Name)
		}
		return client.Credential{}, withExitCode(exitUsage, fmt.Errorf(
			"this organisation has %d GitHub App credentials, and related repositories are stated per "+
				"installation; pass --credential with one of: %s", len(appCredentials), strings.Join(names, ", ")))
	}
}

// relationshipsBetween returns every stored row relating the two repositories,
// in either order. Paths compare the way the platform stores them:
// case-insensitively, ignoring surrounding whitespace and slashes.
func relationshipsBetween(related []client.RelatedRepository, repository string,
	relatedRepository string) []client.RelatedRepository {
	first, second := normaliseRepositoryPath(repository), normaliseRepositoryPath(relatedRepository)
	matches := make([]client.RelatedRepository, 0, 2)
	for _, relationship := range related {
		stored := normaliseRepositoryPath(relationship.RepoFullName)
		storedRelated := normaliseRepositoryPath(relationship.RelatedRepoFullName)
		if (stored == first && storedRelated == second) || (stored == second && storedRelated == first) {
			matches = append(matches, relationship)
		}
	}
	return matches
}

func normaliseRepositoryPath(path string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(path), "/"))
}

// renderRelatedRepositories prints the relationships stated under one
// installation. An empty listing is a successful read that found nothing, so
// it says so, and reminds that co-installed applications need no statement.
func renderRelatedRepositories(out io.Writer, installation relatedReposInstallation,
	related []client.RelatedRepository) {
	if len(related) == 0 {
		_, _ = fmt.Fprintf(out, "No related repositories are stated under %s.\n", installation.describe())
		_, _ = fmt.Fprintln(out, "Applications Ankra installs on the same cluster are still related with no configuration.")
		return
	}
	_, _ = fmt.Fprintf(out, "Related repositories stated under %s:\n", installation.describe())
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "Repository", "Related To"})
	for _, relationship := range related {
		writer.AppendRow(table.Row{relationship.ID, relationship.RepoFullName, relationship.RelatedRepoFullName})
	}
	writer.Render()
}

func init() {
	registerStructuredOutputFlags(orgAIReviewRelatedReposListCmd, orgAIReviewRelatedReposAddCmd)
	for _, command := range []*cobra.Command{
		orgAIReviewRelatedReposListCmd, orgAIReviewRelatedReposAddCmd, orgAIReviewRelatedReposRemoveCmd,
	} {
		command.Flags().String("credential", "",
			"GitHub App credential (name or ID) whose installation the relationships belong to; "+
				"defaults to the organisation's only one")
	}
	orgAIReviewRelatedReposRemoveCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	orgAIReviewRelatedReposCmd.AddCommand(orgAIReviewRelatedReposListCmd, orgAIReviewRelatedReposAddCmd,
		orgAIReviewRelatedReposRemoveCmd)
	orgAIReviewCmd.AddCommand(orgAIReviewRelatedReposCmd)
	orgCmd.AddCommand(orgAIReviewCmd)
}
