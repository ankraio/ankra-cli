package cmd

// The pipeline commands around a run rather than of one: the approvals inbox
// ('pipeline authority'), the organisation's pipeline policy, a repository's
// cache volumes and its workspace profiles.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

type pipelineAuthorityRequestSummary struct {
	ID                string  `json:"id"`
	RepositoryID      string  `json:"repository_id"`
	DefinitionID      string  `json:"definition_id"`
	RunID             string  `json:"run_id"`
	HeadSHA           string  `json:"head_sha"`
	Ref               string  `json:"ref"`
	PullRequestNumber *int    `json:"pull_request_number"`
	RequestedBy       string  `json:"requested_by"`
	Status            string  `json:"status"`
	ResolvedBy        *string `json:"resolved_by"`
	ResolvedAt        *string `json:"resolved_at"`
	Reason            *string `json:"reason"`
	CreatedAt         string  `json:"created_at"`
	Diff              struct {
		Sections []struct {
			Section string  `json:"section"`
			Path    string  `json:"path"`
			From    *string `json:"from"`
			To      *string `json:"to"`
		} `json:"sections"`
	} `json:"diff"`
}

func registerPipelinePageFlags(command *cobra.Command) {
	command.Flags().Int("limit", 50, fmt.Sprintf("How many to list (1-%d)", client.PipelinePageCap))
	command.Flags().String("cursor", "", "Continue from a previous page's next_cursor")
}

func pipelinePageFlags(command *cobra.Command) (string, int, error) {
	limit, _ := command.Flags().GetInt("limit")
	if limit < 1 || limit > client.PipelinePageCap {
		return "", 0, withExitCode(exitUsage, fmt.Errorf("--limit must be between 1 and %d", client.PipelinePageCap))
	}
	cursor, _ := command.Flags().GetString("cursor")
	return strings.TrimSpace(cursor), limit, nil
}

func printPipelineNextCursor(out io.Writer, nextCursor *string) {
	if nextCursor != nil && *nextCursor != "" {
		_, _ = fmt.Fprintf(out, "More follow: pass --cursor %s\n", *nextCursor)
	}
}

func newPipelineAuthorityCommand() *cobra.Command {
	authorityCommand := &cobra.Command{
		Use:   "authority",
		Short: "Work the approvals inbox of pipeline authority requests",
		Long: `Read and dismiss pipeline authority requests: what a run raises when its
definition asks for protected settings (services, network, caches, resources,
timeouts, permissions, secrets) no administrator has approved.

Approving a request is approving its definition:
'ankra pipeline definitions approve <definition-id>', which 'get' prints.
'dismiss' declines it with a reason the run's check and run page then show.`,
	}
	authorityCommand.AddCommand(newPipelineAuthorityListCommand(), newPipelineAuthorityGetCommand(),
		newPipelineAuthorityDismissCommand())
	return authorityCommand
}

func newPipelineAuthorityListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List pipeline authority requests, newest first",
		Long: `List pipeline authority requests, newest first. It lists the pending ones
unless --status names another (approved, dismissed, superseded) or all.
--application or --repository narrows it to one pipeline; without either it
reads the whole organisation. Requires pipelines.read.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cursor, limit, pageError := pipelinePageFlags(command)
			if pageError != nil {
				return pageError
			}
			options := client.ListPipelineAuthorityRequestsOptions{Cursor: cursor, Limit: limit}
			if status, _ := command.Flags().GetString("status"); strings.TrimSpace(status) != "all" {
				options.Status = strings.TrimSpace(status)
			}
			applicationReference, _ := command.Flags().GetString("application")
			repositoryID, _ := command.Flags().GetString("repository")
			if strings.TrimSpace(applicationReference) != "" || strings.TrimSpace(repositoryID) != "" {
				selector, selectorError := resolvePipelineSelector(command)
				if selectorError != nil {
					return selectorError
				}
				options.ApplicationID, options.RepositoryID = selector.ApplicationID, selector.RepositoryID
			}
			document, listError := apiClient.ListPipelineAuthorityRequests(command.Context(), options)
			if listError != nil {
				return listError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var page struct {
					Items      []pipelineAuthorityRequestSummary `json:"items"`
					NextCursor *string                           `json:"next_cursor"`
				}
				if decodeError := decodeDocument(document, &page); decodeError != nil {
					return decodeError
				}
				if len(page.Items) == 0 {
					_, _ = fmt.Fprintln(out, "No pipeline authority requests match.")
					return nil
				}
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"ID", "STATUS", "REF", "REQUESTED BY", "ASKS FOR", "RAISED"})
				for _, request := range page.Items {
					sections := []string{}
					isListed := map[string]bool{}
					for _, section := range request.Diff.Sections {
						if !isListed[section.Section] {
							isListed[section.Section] = true
							sections = append(sections, section.Section)
						}
					}
					writer.AppendRow(table.Row{request.ID, request.Status, truncateCell(request.Ref, 40), request.RequestedBy,
						truncateCell(strings.Join(sections, ", "), 50), request.CreatedAt})
				}
				writer.Render()
				printPipelineNextCursor(out, page.NextCursor)
				return nil
			})
		},
	}
	registerPipelineSelectorFlags(listCommand)
	registerPipelinePageFlags(listCommand)
	listCommand.Flags().String("status", "pending", "Which requests: pending, approved, dismissed, superseded or all")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func printPipelineAuthorityRequest(out io.Writer, document json.RawMessage) error {
	var request pipelineAuthorityRequestSummary
	if decodeError := decodeDocument(document, &request); decodeError != nil {
		return decodeError
	}
	_, _ = fmt.Fprintf(out, "Request:      %s\n", request.ID)
	_, _ = fmt.Fprintf(out, "Status:       %s\n", request.Status)
	_, _ = fmt.Fprintf(out, "Repository:   %s\n", request.RepositoryID)
	_, _ = fmt.Fprintf(out, "Run:          %s\n", request.RunID)
	_, _ = fmt.Fprintf(out, "Commit:       %s on %s\n", request.HeadSHA, request.Ref)
	if request.PullRequestNumber != nil {
		_, _ = fmt.Fprintf(out, "Pull request: #%d\n", *request.PullRequestNumber)
	}
	_, _ = fmt.Fprintf(out, "Requested by: %s\n", request.RequestedBy)
	_, _ = fmt.Fprintf(out, "Definition:   %s\n", request.DefinitionID)
	if request.ResolvedAt != nil {
		_, _ = fmt.Fprintf(out, "Resolved:     %s by %s\n", *request.ResolvedAt, valueOrDash(request.ResolvedBy))
	}
	if request.Reason != nil && *request.Reason != "" {
		_, _ = fmt.Fprintf(out, "Reason:       %s\n", *request.Reason)
	}
	if len(request.Diff.Sections) > 0 {
		_, _ = fmt.Fprintln(out, "\nAsks for:")
		for _, section := range request.Diff.Sections {
			_, _ = fmt.Fprintf(out, "  %s %s: %s -> %s\n", section.Section, section.Path,
				valueOrDash(section.From), valueOrDash(section.To))
		}
	}
	if request.Status == "pending" {
		_, _ = fmt.Fprintf(out, "\nApprove: ankra pipeline definitions approve %s\nDecline: ankra pipeline authority dismiss %s --reason \"...\"\n",
			request.DefinitionID, request.ID)
	}
	return nil
}

func newPipelineAuthorityGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <request-id>",
		Short: "Show one pipeline authority request and what it asks for",
		Long: `Show one request: the run that raised it, the protected settings it asks for
against the approved authority, where it stands and who decided it. -o json
also carries the requesting definition's protected settings as YAML.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			document, getError := apiClient.GetPipelineAuthorityRequest(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				return printPipelineAuthorityRequest(out, document)
			})
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newPipelineAuthorityDismissCommand() *cobra.Command {
	dismissCommand := &cobra.Command{
		Use:   "dismiss <request-id>",
		Short: "Decline a pending pipeline authority request",
		Long: `Decline a pending request with a reason the run's check run and run page then
show. Requires pipelines.manage and a human actor: a service-account token is
refused. A request already approved, dismissed or superseded answers 409.`,
		Example: `  ankra pipeline authority dismiss 0b8f6c2e-0000-4000-8000-000000000001 --reason "use the shared cache size"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			reason, _ := command.Flags().GetString("reason")
			reason = strings.TrimSpace(reason)
			if reason == "" {
				return withExitCode(exitUsage, errors.New("--reason is required: it is what the run's author reads"))
			}
			requestID := strings.TrimSpace(arguments[0])
			skipConfirmation, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Dismiss pipeline authority request %s? [y/N]: ", requestID), skipConfirmation); confirmError != nil {
				return confirmError
			}
			document, dismissError := apiClient.DismissPipelineAuthorityRequest(command.Context(), requestID, reason)
			if dismissError != nil {
				return dismissError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				_, _ = fmt.Fprintf(out, "Dismissed pipeline authority request %s.\n", requestID)
				return nil
			})
		},
	}
	dismissCommand.Flags().String("reason", "", "Why the request is declined (required)")
	dismissCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(dismissCommand)
	return dismissCommand
}

// pipelinePolicyLists are the policy settings written as a list of strings,
// by flag name.
var pipelinePolicyLists = map[string]string{
	"allowed-network-tiers":   "allowed_network_tiers",
	"allowed-credentials":     "allowed_credentials",
	"allowed-runtime-classes": "allowed_runtime_classes",
	"webhook-allowed-hosts":   "webhook_allowed_hosts",
	"external-providers":      "external_providers",
}

func newPipelinePolicyCommand() *cobra.Command {
	policyCommand := &cobra.Command{
		Use:   "policy",
		Short: "Read and change the organisation's pipeline policy",
		Long: `Read and change the organisation's pipeline policy: the ceiling every
pipeline's authority is held to - which network tiers, credentials, node
selectors, tolerations and runtime classes its pipelines may use, how much
CPU, memory, cache and GPU a step may ask for, and which webhook hosts and
external providers are allowed. Both commands need pipelines.manage.`,
	}
	getCommand := &cobra.Command{
		Use:   "get",
		Short: "Show the organisation's pipeline policy",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			document, getError := apiClient.GetPipelinePolicy(command.Context())
			if getError != nil {
				return getError
			}
			return renderDocument(command, document, nil)
		},
	}
	setCommand := &cobra.Command{
		Use:   "set",
		Short: "Change settings of the organisation's pipeline policy",
		Long: `Change the pipeline policy settings you name and leave the rest as they are.
A list flag replaces the whole list; pass it empty (--allowed-credentials "")
to allow none. Node selectors and tolerations are set through -f, which takes
the platform's body as YAML or JSON (- reads stdin); a flag wins over the
document. allowed_image_prefixes belongs to 'ankra org ci-settings' and is not
writable here.

The policy bounds every pipeline of the organisation, so this confirms first;
--yes skips the prompt.`,
		Example: `  ankra pipeline policy set --max-cpu 8 --max-memory 16Gi
  ankra pipeline policy set --allowed-network-tiers restricted,internet --yes
  ankra pipeline policy set -f policy.yaml`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			changes := map[string]any{}
			if path, _ := command.Flags().GetString("file"); path != "" {
				document, readError := readAlertRuleDocument(command)
				if readError != nil {
					return readError
				}
				changes = document
			}
			for flagName, setting := range pipelinePolicyLists {
				if command.Flags().Changed(flagName) {
					values, _ := command.Flags().GetStringSlice(flagName)
					entries := []string{}
					for _, value := range values {
						if value = strings.TrimSpace(value); value != "" {
							entries = append(entries, value)
						}
					}
					changes[setting] = entries
				}
			}
			for _, flagName := range []string{"max-cpu", "max-memory", "max-cache-size"} {
				if command.Flags().Changed(flagName) {
					value, _ := command.Flags().GetString(flagName)
					changes[strings.ReplaceAll(flagName, "-", "_")] = strings.TrimSpace(value)
				}
			}
			for _, flagName := range []string{"max-gpu", "max-agent-tool-calls-per-run"} {
				if command.Flags().Changed(flagName) {
					changes[strings.ReplaceAll(flagName, "-", "_")], _ = command.Flags().GetInt(flagName)
				}
			}
			if len(changes) == 0 {
				return withExitCode(exitUsage, errors.New("nothing to change: name at least one setting, as a flag or in -f"))
			}
			settings := make([]string, 0, len(changes))
			for setting := range changes {
				settings = append(settings, setting)
			}
			sort.Strings(settings)
			skipConfirmation, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Change the organisation's pipeline policy (%s)? It bounds every pipeline. [y/N]: ",
					strings.Join(settings, ", ")), skipConfirmation); confirmError != nil {
				return confirmError
			}
			document, updateError := apiClient.UpdatePipelinePolicy(command.Context(), changes)
			if updateError != nil {
				return updateError
			}
			return renderDocument(command, document, nil)
		},
	}
	for flagName := range pipelinePolicyLists {
		setCommand.Flags().StringSlice(flagName, nil, "Replace the "+strings.ReplaceAll(flagName, "-", " ")+" list; comma-separated or repeatable")
	}
	setCommand.Flags().String("max-cpu", "", "Most CPU a step may ask for, as a Kubernetes quantity")
	setCommand.Flags().String("max-memory", "", "Most memory a step may ask for, as a Kubernetes quantity")
	setCommand.Flags().String("max-cache-size", "", "Largest cache volume a stage may declare, as a Kubernetes quantity")
	setCommand.Flags().Int("max-gpu", 0, "Most GPUs a step may ask for")
	setCommand.Flags().Int("max-agent-tool-calls-per-run", 0, "Most tool calls an agent stage may make in one run")
	setCommand.Flags().StringP("file", "f", "", "YAML or JSON file with the settings to change (- reads stdin)")
	setCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(getCommand, setCommand)
	policyCommand.AddCommand(getCommand, setCommand)
	return policyCommand
}

func newPipelineCachesCommand() *cobra.Command {
	cachesCommand := &cobra.Command{
		Use:     "caches",
		Aliases: []string{"cache"},
		Short:   "List the cache volumes a pipeline keeps between runs",
		Long: `List the keyed cache volumes a pipeline repository keeps between runs, most
recently used first: the key a stage declared, where it is mounted, its size,
the cluster the volume lives on, and when it lapses the organisation's cache
retention. A cache is scoped to the default branch or to every other branch;
the two never share a volume. Requires pipelines.read.`,
		Example: `  ankra pipeline caches --application my-app
  ankra pipeline caches --repository 5f0c1d6e-0000-4000-8000-000000000001 -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cursor, limit, pageError := pipelinePageFlags(command)
			if pageError != nil {
				return pageError
			}
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			document, listError := apiClient.ListPipelineCaches(command.Context(), selector, cursor, limit)
			if listError != nil {
				return listError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var page struct {
					Caches []struct {
						Key         string `json:"key"`
						MountPath   string `json:"mount_path"`
						Trust       string `json:"trust"`
						Size        string `json:"size"`
						ClusterName string `json:"cluster_name"`
						LastUsedAt  string `json:"last_used_at"`
						ExpiresAt   string `json:"expires_at"`
						State       string `json:"state"`
					} `json:"caches"`
					RetentionDays int     `json:"retention_days"`
					NextCursor    *string `json:"next_cursor"`
				}
				if decodeError := decodeDocument(document, &page); decodeError != nil {
					return decodeError
				}
				if len(page.Caches) == 0 {
					_, _ = fmt.Fprintln(out, "This pipeline keeps no caches.")
					return nil
				}
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"KEY", "MOUNT", "SCOPE", "SIZE", "CLUSTER", "LAST USED", "EXPIRES", "STATE"})
				for _, cache := range page.Caches {
					writer.AppendRow(table.Row{truncateCell(cache.Key, 50), cache.MountPath, cache.Trust, cache.Size,
						orDash(cache.ClusterName), cache.LastUsedAt, cache.ExpiresAt, cache.State})
				}
				writer.Render()
				_, _ = fmt.Fprintf(out, "A cache lapses %d days after its last use.\n", page.RetentionDays)
				printPipelineNextCursor(out, page.NextCursor)
				return nil
			})
		},
	}
	registerPipelineSelectorFlags(cachesCommand)
	registerPipelinePageFlags(cachesCommand)
	registerStructuredOutputFlags(cachesCommand)
	return cachesCommand
}

// resolvePipelineRepositoryID answers the pipeline repository the selector
// flags name. The workspace profile routes exist only under the repository,
// so an application is resolved to the repository its pipeline lives in.
func resolvePipelineRepositoryID(command *cobra.Command) (string, error) {
	selector, selectorError := resolvePipelineSelector(command)
	if selectorError != nil {
		return "", selectorError
	}
	if selector.RepositoryID != "" {
		return selector.RepositoryID, nil
	}
	definition, definitionError := apiClient.GetPipelineDefinition(command.Context(), selector)
	if definitionError != nil {
		return "", fmt.Errorf("finding the application's pipeline repository: %w", definitionError)
	}
	if definition == nil || definition.Repository.ID == "" {
		return "", errors.New("the platform named no pipeline repository for this application")
	}
	return definition.Repository.ID, nil
}

func newPipelineWorkspaceProfilesCommand() *cobra.Command {
	profilesCommand := &cobra.Command{
		Use:   "workspace-profiles",
		Short: "Read and replace a repository's workspace profiles",
		Long: `Read and replace a repository's workspace profiles: what its workspace of each
kind runs - the image, the size, the service sidecars, the setup commands and
the pinned tools. 'ankra dev' and 'ankra workspace' bring up workspaces from
them; a repository with none takes its workspace image from its pipeline.

A caller with workspaces.manage reads the profiles whole; one with only
workspaces.use reads which kinds exist, each as an empty profile.`,
	}
	getCommand := &cobra.Command{
		Use:   "get",
		Short: "Show a repository's workspace profiles",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			repositoryID, repositoryError := resolvePipelineRepositoryID(command)
			if repositoryError != nil {
				return repositoryError
			}
			document, getError := apiClient.GetWorkspaceProfilesDocument(command.Context(), repositoryID)
			if getError != nil {
				return getError
			}
			return renderDocument(command, document, nil)
		},
	}
	putCommand := &cobra.Command{
		Use:   "put",
		Short: "Replace a repository's workspace profiles from a file",
		Long: `Replace the repository's workspace profiles with the ones in -f: a YAML or
JSON object keyed by kind (- reads stdin), or the document 'get' prints, whose
profiles member is used. An empty object clears them. Every profile is
validated as a workspace would be rendered from it. A running workspace picks
the change up at its owner's next bring-up, which replaces it.

Requires workspaces.manage. The file replaces every profile, so this confirms
first; --yes skips the prompt.`,
		Example: `  ankra pipeline workspace-profiles get --application my-app -o yaml > profiles.yaml
  ankra pipeline workspace-profiles put --application my-app -f profiles.yaml`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			if path, _ := command.Flags().GetString("file"); path == "" {
				return withExitCode(exitUsage, errors.New("-f is required: the file with the profiles keyed by kind"))
			}
			profiles, readError := readAlertRuleDocument(command)
			if readError != nil {
				return readError
			}
			if nested, isDocument := profiles["profiles"].(map[string]any); isDocument {
				if _, hasRepository := profiles["repository_id"]; hasRepository || len(profiles) == 1 {
					profiles = nested
				}
			}
			repositoryID, repositoryError := resolvePipelineRepositoryID(command)
			if repositoryError != nil {
				return repositoryError
			}
			kinds := make([]string, 0, len(profiles))
			for kind := range profiles {
				kinds = append(kinds, kind)
			}
			sort.Strings(kinds)
			summary := "no profiles, clearing the ones stored"
			if len(kinds) > 0 {
				summary = "the profiles " + strings.Join(kinds, ", ")
			}
			skipConfirmation, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Replace the workspace profiles of repository %s with %s? [y/N]: ", repositoryID, summary),
				skipConfirmation); confirmError != nil {
				return confirmError
			}
			document, putError := apiClient.PutWorkspaceProfiles(command.Context(), repositoryID, profiles)
			if putError != nil {
				return putError
			}
			return renderDocument(command, document, nil)
		},
	}
	putCommand.Flags().StringP("file", "f", "", "YAML or JSON file with the profiles keyed by kind (- reads stdin)")
	putCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	for _, selecting := range []*cobra.Command{getCommand, putCommand} {
		registerPipelineSelectorFlags(selecting)
	}
	registerStructuredOutputFlags(getCommand, putCommand)
	profilesCommand.AddCommand(getCommand, putCommand)
	return profilesCommand
}
