package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var orgAIReviewReposCmd = &cobra.Command{
	Use:     "repos",
	Aliases: []string{"repositories"},
	Short:   "Turn the AI code review on or off for one repository",
	Long: `Turn the AI code review on or off for one repository.

Each source-control connection (a GitHub App installation, or a GitLab or
Bitbucket credential) has binding-level AI review settings that every
repository on it inherits. A repository rule replaces them for that one
repository: whether the review runs, whether the AI answers @mentions, and
whether pull requests get previews, plus optionally the review model, draft
reviews and the per-pull-request review cap.

  ankra org ai-review repos list
  ankra org ai-review repos set my-org/my-repo --review --mentions
  ankra org ai-review repos unset my-org/my-repo

'set' changes only the switches you pass: the rest keep the repository's
current rule, or the connection's settings when it has none. 'unset' removes
the rule, so the repository follows the connection's settings again.

The connection is found from the repository: a GitHub App installation whose
account owns it, or the connection that already holds a rule for it. Pass
--binding <provider>/<id> (from 'list') when that is ambiguous.

Listing requires organisation membership; set and unset require organisation
admin.`,
}

var orgAIReviewReposListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the source-control connections, their AI review settings and repository rules",
	Example: `  ankra org ai-review repos list
  ankra org ai-review repos list --reachable --binding github/12345678
  ankra org ai-review repos list -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		requestedBinding, _ := cmd.Flags().GetString("binding")
		reachable, _ := cmd.Flags().GetBool("reachable")

		ctx, cancel := context.WithTimeout(cmd.Context(), relatedReposRequestTimeout)
		defer cancel()

		bindings, listError := apiClient.ListSCMBindings(ctx)
		if listError != nil {
			return fmt.Errorf("listing source-control connections: %w", listError)
		}
		if strings.TrimSpace(requestedBinding) != "" {
			binding, findError := findSCMBinding(bindings, requestedBinding)
			if findError != nil {
				return findError
			}
			bindings = []client.SCMBinding{binding}
		}

		result := scmBindingListing{Bindings: bindings}
		if reachable {
			result.ReachableRepositories = map[string][]client.SCMBindingRepository{}
			for _, binding := range bindings {
				if !strings.EqualFold(binding.Provider, "github") {
					continue
				}
				repositories, reposError := apiClient.ListSCMBindingRepositories(ctx, binding.Provider, binding.BindingExternalID)
				if reposError != nil {
					return fmt.Errorf("listing the repositories %s reaches: %w", scmBindingKey(binding), reposError)
				}
				result.ReachableRepositories[scmBindingKey(binding)] = repositories
			}
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			return renderError
		}
		renderSCMBindings(cmd.OutOrStdout(), result)
		return nil
	},
}

var orgAIReviewReposSetCmd = &cobra.Command{
	Use:   "set <owner/repository>",
	Short: "Set one repository's AI review rule",
	Long: `Set one repository's AI review rule.

Only the switches you pass change. The others keep the repository's current
rule, or the connection's settings when the repository has no rule yet, so
'set my-org/my-repo --review' never switches @mention replies or previews off
by accident.

Requires organisation admin.`,
	Example: `  ankra org ai-review repos set my-org/my-repo --review
  ankra org ai-review repos set my-org/my-repo --review=false
  ankra org ai-review repos set my-org/my-repo --review --mentions --review-drafts --max-reviews-per-pr 3
  ankra org ai-review repos set my-group/sub/project --binding gitlab/0b6f6d1e-6c1d-4f3a-9b43-7f6c2b1e9d10 --previews`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		flags := cmd.Flags()
		if !flags.Changed("review") && !flags.Changed("mentions") && !flags.Changed("previews") &&
			!flags.Changed("model") && !flags.Changed("review-drafts") && !flags.Changed("max-reviews-per-pr") {
			return withExitCode(exitUsage, errors.New(
				"pass at least one of --review, --mentions, --previews, --model, --review-drafts or --max-reviews-per-pr"))
		}
		if maxReviews, _ := flags.GetInt("max-reviews-per-pr"); flags.Changed("max-reviews-per-pr") && maxReviews < 0 {
			return withExitCode(exitUsage, errors.New("--max-reviews-per-pr must be 0 (no cap) or more"))
		}
		repository := args[0]
		requestedBinding, _ := flags.GetString("binding")

		ctx, cancel := context.WithTimeout(cmd.Context(), relatedReposRequestTimeout)
		defer cancel()

		bindings, listError := apiClient.ListSCMBindings(ctx)
		if listError != nil {
			return fmt.Errorf("listing source-control connections: %w", listError)
		}
		binding, resolveError := resolveSCMBindingForRepository(bindings, requestedBinding, repository)
		if resolveError != nil {
			return resolveError
		}

		write := ruleWriteFromFlags(cmd, binding, repository)
		stored, putError := apiClient.PutSCMRepositoryRule(ctx, binding.Provider, binding.BindingExternalID, write)
		if putError != nil {
			return fmt.Errorf("setting the AI review rule for %s: %w", repository, putError)
		}
		if stored.RepoFullName == "" {
			stored.RepoFullName = repository
		}
		if rendered, renderError := renderStructured(cmd, stored); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Set the AI review rule for %s on %s: review %s, @mention replies %s, previews %s.\n",
			stored.RepoFullName, describeSCMBinding(binding), onOff(stored.AIReview),
			onOff(stored.MentionReplies), onOff(stored.PRPreviews))
		return nil
	},
}

var orgAIReviewReposUnsetCmd = &cobra.Command{
	Use:     "unset <owner/repository>",
	Aliases: []string{"remove", "rm"},
	Short:   "Remove one repository's AI review rule, so it follows the connection's settings",
	Example: `  ankra org ai-review repos unset my-org/my-repo
  ankra org ai-review repos unset my-org/my-repo --binding github/12345678 --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		repository := args[0]
		requestedBinding, _ := cmd.Flags().GetString("binding")
		skipConfirmation, _ := cmd.Flags().GetBool("yes")

		ctx, cancel := context.WithTimeout(cmd.Context(), relatedReposRequestTimeout)
		defer cancel()

		bindings, listError := apiClient.ListSCMBindings(ctx)
		if listError != nil {
			return fmt.Errorf("listing source-control connections: %w", listError)
		}
		binding, resolveError := resolveSCMBindingForRepository(bindings, requestedBinding, repository)
		if resolveError != nil {
			return resolveError
		}
		// The listing already carries the connection's rules, so a
		// repository without one fails fast instead of being prompted for
		// a removal that has nothing to remove.
		if !bindingHasRule(binding, repository) {
			return errNoSCMRepositoryRule(repository, binding)
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Remove the AI review rule for %s on %s? [y/N]: ", repository, describeSCMBinding(binding)),
			skipConfirmation); confirmError != nil {
			return confirmError
		}
		deleted, deleteError := apiClient.DeleteSCMRepositoryRule(ctx, binding.Provider, binding.BindingExternalID, repository)
		if deleteError != nil {
			return fmt.Errorf("removing the AI review rule for %s: %w", repository, deleteError)
		}
		if !deleted.Removed {
			return errNoSCMRepositoryRule(repository, binding)
		}
		if rendered, renderError := renderStructured(cmd, deleted); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed the AI review rule for %s; it follows %s's settings again.\n",
			repository, describeSCMBinding(binding))
		return nil
	},
}

// scmBindingListing is the structured shape of 'repos list'.
type scmBindingListing struct {
	Bindings              []client.SCMBinding                      `json:"bindings" yaml:"bindings"`
	ReachableRepositories map[string][]client.SCMBindingRepository `json:"reachable_repositories,omitempty" yaml:"reachable_repositories,omitempty"`
}

func bindingHasRule(binding client.SCMBinding, repository string) bool {
	normalized := normaliseRepositoryPath(repository)
	for _, rule := range binding.RepoOverrides {
		if normaliseRepositoryPath(rule.RepoFullName) == normalized {
			return true
		}
	}
	return false
}

func errNoSCMRepositoryRule(repository string, binding client.SCMBinding) error {
	return withExitCode(exitNotFound, fmt.Errorf(
		"%s has no AI review rule on %s; it already follows the connection's settings",
		repository, describeSCMBinding(binding)))
}

func scmBindingKey(binding client.SCMBinding) string {
	return binding.Provider + "/" + binding.BindingExternalID
}

func describeSCMBinding(binding client.SCMBinding) string {
	if binding.DisplayName == "" {
		return scmBindingKey(binding)
	}
	return fmt.Sprintf("%s (%s)", binding.DisplayName, scmBindingKey(binding))
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

// findSCMBinding resolves a --binding value, <provider>/<binding id>.
func findSCMBinding(bindings []client.SCMBinding, requested string) (client.SCMBinding, error) {
	requested = strings.TrimSpace(requested)
	provider, externalID, hasSeparator := strings.Cut(requested, "/")
	if !hasSeparator || provider == "" || externalID == "" {
		return client.SCMBinding{}, withExitCode(exitUsage, fmt.Errorf(
			"--binding takes <provider>/<id> as 'ankra org ai-review repos list' prints it, e.g. github/12345678; got %q",
			requested))
	}
	for _, binding := range bindings {
		if strings.EqualFold(binding.Provider, provider) && binding.BindingExternalID == externalID {
			return binding, nil
		}
	}
	return client.SCMBinding{}, withExitCode(exitNotFound, fmt.Errorf(
		"no source-control connection %s in this organisation; run 'ankra org ai-review repos list'", requested))
}

// resolveSCMBindingForRepository picks the connection a repository rule is
// written on: --binding when given; otherwise the one connection that
// already holds a rule for the repository; otherwise the one GitHub App
// installation whose account owns the repository; otherwise the
// organisation's only connection when it is not a GitHub one (GitLab and
// Bitbucket connections carry no owner to match). Anything else refuses
// rather than guessing, because a rule on the wrong connection is stored
// and never applied.
func resolveSCMBindingForRepository(bindings []client.SCMBinding, requested string, repository string) (client.SCMBinding, error) {
	if strings.TrimSpace(requested) != "" {
		return findSCMBinding(bindings, requested)
	}
	normalized := normaliseRepositoryPath(repository)
	owner, _, hasOwner := strings.Cut(normalized, "/")
	if !hasOwner || owner == "" {
		return client.SCMBinding{}, withExitCode(exitUsage, fmt.Errorf(
			"%q is not an owner/repository path", repository))
	}
	if len(bindings) == 0 {
		return client.SCMBinding{}, errors.New(
			"this organisation has no source-control connection; connect GitHub, GitLab or Bitbucket first")
	}

	var withRule []client.SCMBinding
	for _, binding := range bindings {
		for _, rule := range binding.RepoOverrides {
			if normaliseRepositoryPath(rule.RepoFullName) == normalized {
				withRule = append(withRule, binding)
				break
			}
		}
	}
	if len(withRule) == 1 {
		return withRule[0], nil
	}
	if len(withRule) == 0 {
		var ownerMatches []client.SCMBinding
		for _, binding := range bindings {
			if strings.EqualFold(binding.Provider, "github") && strings.EqualFold(binding.DisplayName, owner) {
				ownerMatches = append(ownerMatches, binding)
			}
		}
		if len(ownerMatches) == 1 {
			return ownerMatches[0], nil
		}
		if len(ownerMatches) == 0 && len(bindings) == 1 && !strings.EqualFold(bindings[0].Provider, "github") {
			return bindings[0], nil
		}
	}

	keys := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		keys = append(keys, describeSCMBinding(binding))
	}
	return client.SCMBinding{}, withExitCode(exitUsage, fmt.Errorf(
		"cannot tell which source-control connection %s belongs to; pass --binding with one of: %s",
		repository, strings.Join(keys, ", ")))
}

// ruleWriteFromFlags builds the PUT body. The platform requires all three
// switches on every write, so each one the caller did not pass is filled
// from the repository's current rule, or the connection's settings when it
// has none. The optional fields are sent only when passed, which leaves
// the stored values untouched.
func ruleWriteFromFlags(cmd *cobra.Command, binding client.SCMBinding, repository string) client.SCMRepositoryRuleWrite {
	write := client.SCMRepositoryRuleWrite{
		RepoFullName:   repository,
		AIReview:       binding.AIReview,
		MentionReplies: binding.MentionReplies,
		PRPreviews:     binding.PRPreviews,
	}
	normalized := normaliseRepositoryPath(repository)
	for _, rule := range binding.RepoOverrides {
		if normaliseRepositoryPath(rule.RepoFullName) == normalized {
			write.AIReview, write.MentionReplies, write.PRPreviews = rule.AIReview, rule.MentionReplies, rule.PRPreviews
			break
		}
	}
	flags := cmd.Flags()
	if flags.Changed("review") {
		write.AIReview, _ = flags.GetBool("review")
	}
	if flags.Changed("mentions") {
		write.MentionReplies, _ = flags.GetBool("mentions")
	}
	if flags.Changed("previews") {
		write.PRPreviews, _ = flags.GetBool("previews")
	}
	if flags.Changed("model") {
		model, _ := flags.GetString("model")
		write.ReviewModel = &model
	}
	if flags.Changed("review-drafts") {
		reviewDrafts, _ := flags.GetBool("review-drafts")
		write.ReviewDrafts = &reviewDrafts
	}
	if flags.Changed("max-reviews-per-pr") {
		maxReviews, _ := flags.GetInt("max-reviews-per-pr")
		write.MaxReviewsPerPR = &maxReviews
	}
	return write
}

func renderSCMBindings(out io.Writer, listing scmBindingListing) {
	if len(listing.Bindings) == 0 {
		_, _ = fmt.Fprintln(out, "No source-control connections. Connect GitHub, GitLab or Bitbucket first.")
		return
	}
	modelOrDefault := func(model string, fallback string) string {
		if model == "" {
			return fallback
		}
		return model
	}
	capOrNone := func(maxReviews int) string {
		if maxReviews == 0 {
			return "no cap"
		}
		return fmt.Sprintf("%d", maxReviews)
	}
	for index, binding := range listing.Bindings {
		if index > 0 {
			_, _ = fmt.Fprintln(out)
		}
		_, _ = fmt.Fprintf(out, "%s - %s\n", describeSCMBinding(binding), binding.Detail)
		_, _ = fmt.Fprintf(out, "  Connection settings: review %s, @mention replies %s, previews %s, model %s, drafts %s, max reviews per PR %s\n",
			onOff(binding.AIReview), onOff(binding.MentionReplies), onOff(binding.PRPreviews),
			modelOrDefault(binding.ReviewModel, "platform default"), onOff(binding.ReviewDrafts),
			capOrNone(binding.MaxReviewsPerPR))
		if len(binding.RepoOverrides) == 0 {
			_, _ = fmt.Fprintln(out, "  No repository rules: every repository follows the connection settings.")
		} else {
			writer := table.NewWriter()
			writer.SetOutputMirror(out)
			writer.SetStyle(table.StyleRounded)
			writer.AppendHeader(table.Row{"Repository", "Review", "Mentions", "Previews", "Model", "Drafts", "Max Reviews"})
			for _, rule := range binding.RepoOverrides {
				writer.AppendRow(table.Row{rule.RepoFullName, onOff(rule.AIReview), onOff(rule.MentionReplies),
					onOff(rule.PRPreviews), modelOrDefault(rule.ReviewModel, "connection's"), onOff(rule.ReviewDrafts),
					capOrNone(rule.MaxReviewsPerPR)})
			}
			writer.Render()
		}
		if repositories, listed := listing.ReachableRepositories[scmBindingKey(binding)]; listed {
			_, _ = fmt.Fprintf(out, "  Reachable repositories (%d):\n", len(repositories))
			for _, repository := range repositories {
				_, _ = fmt.Fprintf(out, "    %s\n", repository.RepoFullName)
			}
		}
	}
}

func init() {
	registerStructuredOutputFlags(orgAIReviewReposListCmd, orgAIReviewReposSetCmd, orgAIReviewReposUnsetCmd)
	for _, command := range []*cobra.Command{orgAIReviewReposListCmd, orgAIReviewReposSetCmd, orgAIReviewReposUnsetCmd} {
		command.Flags().String("binding", "",
			"Source-control connection as <provider>/<id> from 'list' (default: found from the repository)")
	}
	orgAIReviewReposListCmd.Flags().Bool("reachable", false,
		"Also list the repositories each GitHub App installation reaches")
	setFlags := orgAIReviewReposSetCmd.Flags()
	setFlags.Bool("review", false, "Run the AI review on this repository's pull requests (--review=false to stop)")
	setFlags.Bool("mentions", false, "Answer @mentions on this repository (--mentions=false to stop)")
	setFlags.Bool("previews", false, "Build pull request previews for this repository (--previews=false to stop)")
	setFlags.String("model", "", "Review model (catalogue key); an empty value inherits the connection's model")
	setFlags.Bool("review-drafts", false, "Review draft pull requests automatically too (--review-drafts=false to skip them)")
	setFlags.Int("max-reviews-per-pr", 0, "Cap on automatic reviews per pull request (0 = no cap)")
	orgAIReviewReposUnsetCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	orgAIReviewReposCmd.AddCommand(orgAIReviewReposListCmd, orgAIReviewReposSetCmd, orgAIReviewReposUnsetCmd)
	orgAIReviewCmd.AddCommand(orgAIReviewReposCmd)
}
