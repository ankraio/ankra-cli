package cmd

// `ankra registry connections discover|verify`, `ankra credentials propagation`,
// `ankra ai memories profile ...` and `ankra application autonomy actions`
// (ankra-hq41xb.23): reads and checks the API serves that had no command.
// None of them carries secret material: a registry check names the admin
// credential, and the platform acts as it.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func printRegistryConnectionCheck(out io.Writer, document json.RawMessage) error {
	var check struct {
		OK           bool   `json:"ok"`
		Detail       string `json:"detail"`
		Host         string `json:"host"`
		Provider     string `json:"provider"`
		ProjectCount *int   `json:"project_count"`
		Truncated    bool   `json:"truncated"`
		Projects     []struct {
			Name            string `json:"name"`
			Public          bool   `json:"public"`
			RepositoryCount *int   `json:"repository_count"`
		} `json:"projects"`
	}
	if decodeError := decodeDocument(document, &check); decodeError != nil {
		return decodeError
	}
	_, _ = fmt.Fprintf(out, "Registry: %s (%s)\n", orDash(check.Host), orDash(check.Provider))
	_, _ = fmt.Fprintf(out, "%s\n", check.Detail)
	if check.ProjectCount != nil {
		_, _ = fmt.Fprintf(out, "The credential sees %s.\n", pluralCount(*check.ProjectCount, "project"))
	}
	if len(check.Projects) > 0 {
		rows := make([]table.Row, 0, len(check.Projects))
		for _, project := range check.Projects {
			repositories := "-"
			if project.RepositoryCount != nil {
				repositories = fmt.Sprintf("%d", *project.RepositoryCount)
			}
			rows = append(rows, table.Row{project.Name, yesNo(project.Public), repositories})
		}
		renderRoundedTable(out, table.Row{"Project", "Public", "Repositories"}, rows)
	}
	if check.Truncated {
		_, _ = fmt.Fprintln(out, "The registry holds more than the 500 projects listed.")
	}
	return nil
}

func newRegistryConnectionsCommand() *cobra.Command {
	connectionsCommand := &cobra.Command{
		Use:     "connections",
		Aliases: []string{"connection"},
		Short:   "Check what an admin credential can do on a registry of your own",
		Long: `Check a registry of your own (Harbor, Google Artifact Registry) before Ankra
makes robots there. Ankra acts as an admin credential the organisation
already stores, named with --admin-credential ('ankra helm credentials
list'); the credential's secret is never sent from here. Nothing is written
or minted. Needs credentials.write and credentials.reveal.

A check the registry fails (the credential refused, the registry unreachable)
prints why and exits 1.`,
	}

	registryCheck := func(use string, short string, long string, example string, subjectFlag string, subjectMeaning string,
		invoke func(command *cobra.Command, subject string, adminCredentialName string) (json.RawMessage, error)) *cobra.Command {
		checkCommand := &cobra.Command{
			Use:     use,
			Short:   short,
			Long:    long,
			Example: example,
			Args:    cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				if _, formatError := structuredFormatFromFlags(command); formatError != nil {
					return formatError
				}
				subject, subjectError := requiredTextFlag(command, subjectFlag, subjectMeaning)
				if subjectError != nil {
					return subjectError
				}
				adminCredentialName, credentialError := requiredTextFlag(command, "admin-credential",
					"the name of the organisation's registry credential to act as")
				if credentialError != nil {
					return credentialError
				}
				document, checkError := invoke(command, subject, adminCredentialName)
				if checkError != nil {
					return checkError
				}
				if renderError := renderDocument(command, document, func(out io.Writer) error {
					return printRegistryConnectionCheck(out, document)
				}); renderError != nil {
					return renderError
				}
				var outcome struct {
					OK bool `json:"ok"`
				}
				if decodeError := decodeDocument(document, &outcome); decodeError != nil {
					return decodeError
				}
				if !outcome.OK {
					return withExitCode(exitError, fmt.Errorf("the registry connection did not pass; the reason is above"))
				}
				return nil
			},
		}
		checkCommand.Flags().String(subjectFlag, "", subjectMeaning+" (required)")
		checkCommand.Flags().String("admin-credential", "", "The organisation's registry credential to act as, by name (required)")
		registerStructuredOutputFlags(checkCommand)
		return checkCommand
	}

	connectionsCommand.AddCommand(
		registryCheck("discover --url <registry> --admin-credential <name>",
			"List the projects an admin credential sees on a registry",
			`List what an admin credential sees on a registry before any registry entry
points at it: the projects (Harbor, up to 500) or the Docker repositories
(Google Artifact Registry) robots and registry entries can be made for. A
credential the platform manages is refused.`,
			"  ankra registry connections discover --url oci://harbor.example.com --admin-credential harbor-admin",
			"url", "The registry, as a host or an oci:// or https:// URL",
			func(command *cobra.Command, subject string, adminCredentialName string) (json.RawMessage, error) {
				return apiClient.DiscoverRegistryConnection(command.Context(), subject, adminCredentialName)
			}),
		registryCheck("verify --registry <name> --admin-credential <name>",
			"Check that an admin credential can manage robots on a registry entry",
			`Check that an admin credential can manage robots on an integrated registry
entry ('ankra helm registries list'), before a robot is made there.`,
			"  ankra registry connections verify --registry harbor --admin-credential harbor-admin",
			"registry", "The integrated OCI registry entry to verify, by name",
			func(command *cobra.Command, subject string, adminCredentialName string) (json.RawMessage, error) {
				return apiClient.VerifyRegistryConnection(command.Context(), subject, adminCredentialName)
			}),
	)
	return connectionsCommand
}

func newCredentialsPropagationCommand() *cobra.Command {
	return newOperationCommand(operationCommand{
		use:   "propagation <credential>",
		short: "Show where a rotated credential has reached, and whether the old value is safe to revoke",
		long: `Show where the credential's latest rotation has got to in each cluster linked
to it. The old value is safe to revoke once every running cluster whose copy
Ankra renders serves the new one; the clusters that block it are named, and a
cluster Ankra does not cover carries the command that updates its copy by
hand. The answer carries no secret material.`,
		example:   "  ankra credentials propagation hetzner-prod\n  ankra credentials propagation hetzner-prod -o json",
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			credentialID, resolveError := resolveCredentialID(strings.TrimSpace(arguments[0]))
			if resolveError != nil {
				return nil, resolveError
			}
			return apiClient.GetCredentialPropagation(command.Context(), credentialID)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var propagation struct {
				RotationID       *string `json:"rotation_id"`
				Revision         int     `json:"revision"`
				RotatedAt        *string `json:"rotated_at"`
				SafeToRevoke     bool    `json:"safe_to_revoke"`
				BlockingClusters []struct {
					ClusterName string `json:"cluster_name"`
				} `json:"blocking_clusters"`
				Clusters []struct {
					ClusterName string  `json:"cluster_name"`
					Coverage    string  `json:"coverage"`
					State       string  `json:"state"`
					Reason      *string `json:"reason"`
					Remedy      *string `json:"remedy"`
				} `json:"clusters"`
			}
			if decodeError := decodeDocument(document, &propagation); decodeError != nil {
				return decodeError
			}
			if propagation.RotationID == nil {
				_, _ = fmt.Fprintf(out, "No rotation of this credential is recorded (revision %d).\n", propagation.Revision)
			} else {
				_, _ = fmt.Fprintf(out, "Revision %d, rotated %s.\n", propagation.Revision, valueOrDash(propagation.RotatedAt))
			}
			if propagation.SafeToRevoke {
				_, _ = fmt.Fprintln(out, "The old value is safe to revoke.")
			} else {
				blocking := "clusters still block"
				if len(propagation.BlockingClusters) == 1 {
					blocking = "cluster still blocks"
				}
				_, _ = fmt.Fprintf(out, "The old value is NOT safe to revoke yet: %d %s it.\n", len(propagation.BlockingClusters), blocking)
			}
			if len(propagation.Clusters) == 0 {
				return nil
			}
			rows := make([]table.Row, 0, len(propagation.Clusters))
			for _, cluster := range propagation.Clusters {
				rows = append(rows, table.Row{cluster.ClusterName, cluster.Coverage, cluster.State, valueOrDash(cluster.Reason)})
			}
			renderRoundedTable(out, table.Row{"Cluster", "Coverage", "State", "Reason"}, rows)
			for _, cluster := range propagation.Clusters {
				if cluster.Remedy != nil && *cluster.Remedy != "" {
					_, _ = fmt.Fprintf(out, "%s: %s\n", cluster.ClusterName, *cluster.Remedy)
				}
			}
			return nil
		},
	})
}

func printAIMemoryProfile(out io.Writer, document json.RawMessage) error {
	var learned struct {
		Profile         map[string]any `json:"profile"`
		LearningEnabled bool           `json:"learning_enabled"`
		TurnsObserved   int            `json:"turns_observed"`
		LastExtractedAt *string        `json:"last_extracted_at"`
	}
	if decodeError := decodeDocument(document, &learned); decodeError != nil {
		return decodeError
	}
	learning := "off"
	if learned.LearningEnabled {
		learning = "on"
	}
	_, _ = fmt.Fprintf(out, "Learning: %s (%d turns observed, last learned %s)\n", learning, learned.TurnsObserved,
		valueOrDash(learned.LastExtractedAt))
	shown := 0
	for _, member := range []string{"communication_style", "verbosity", "expertise_level", "expertise_areas",
		"preferred_tools", "workflows", "interests", "notes"} {
		text := ""
		switch value := learned.Profile[member].(type) {
		case string:
			text = value
		case []any:
			entries := make([]string, 0, len(value))
			for _, entry := range value {
				entries = append(entries, fmt.Sprint(entry))
			}
			text = strings.Join(entries, "; ")
		}
		if text == "" {
			continue
		}
		_, _ = fmt.Fprintf(out, "  %s: %s\n", strings.ReplaceAll(member, "_", " "), text)
		shown++
	}
	if shown == 0 {
		_, _ = fmt.Fprintln(out, "The assistant has learned nothing about your preferences yet.")
	}
	return nil
}

func newAIMemoriesProfileCommand() *cobra.Command {
	profileCommand := &cobra.Command{
		Use:   "profile",
		Short: "Read, correct or reset what the assistant has learned about how you work",
		Long: `Beside its memories, the assistant learns a profile of you from your
conversations: how you like to be answered, what you know well, the tools
and workflows you prefer. These commands act on your own profile in the
current organisation only.`,
	}

	getCommand := newOperationCommand(operationCommand{
		use:       "get",
		short:     "Show the learned profile and whether learning is on",
		example:   "  ankra ai memories profile get\n  ankra ai memories profile get -o json",
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			return apiClient.GetAIMemoryProfile(command.Context())
		},
		human: printAIMemoryProfile,
	})

	setCommand := newOperationCommand(operationCommand{
		use:   "set [--learning on|off] [--file <profile.json>]",
		short: "Turn learning on or off, or replace the learned profile",
		long: `--learning on|off switches learning. --file replaces the learned profile
with a JSON object ('-' for stdin) whose members are communication_style,
verbosity, expertise_level (text) and expertise_areas, preferred_tools,
workflows, interests, notes (lists of text); start from 'ankra ai memories
profile get -o json' and send its "profile" member back. What you do not pass
keeps its stored value.`,
		example: `  ankra ai memories profile set --learning off
  ankra ai memories profile get -o json | jq .profile > profile.json   # edit it, then:
  ankra ai memories profile set --file profile.json`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			update := client.AIMemoryProfileUpdate{}
			if command.Flags().Changed("learning") {
				learning, _ := command.Flags().GetString("learning")
				if learning != "on" && learning != "off" {
					return nil, withExitCode(exitUsage, fmt.Errorf("--learning must be on or off, got %q", learning))
				}
				enabled := learning == "on"
				update.LearningEnabled = &enabled
			}
			if profilePath, _ := command.Flags().GetString("file"); strings.TrimSpace(profilePath) != "" {
				profile, readError := readJSONObjectFile(command, strings.TrimSpace(profilePath))
				if readError != nil {
					return nil, readError
				}
				update.Profile = profile
			}
			if update.LearningEnabled == nil && update.Profile == nil {
				return nil, withExitCode(exitUsage, fmt.Errorf("nothing to change: pass --learning or --file"))
			}
			return apiClient.UpdateAIMemoryProfile(command.Context(), update)
		},
		human: printAIMemoryProfile,
	})
	setCommand.Flags().String("learning", "", "on or off")
	setCommand.Flags().String("file", "", "A JSON object that replaces the learned profile ('-' for stdin)")

	resetCommand := newOperationCommand(operationCommand{
		use:   "reset",
		short: "Clear what the assistant has learned about your preferences",
		long: `Clear the learned profile. Stored memories are not affected ('ankra ai
memories clear' deletes those). It asks first; --yes skips the question and is
required when there is no terminal to ask on.`,
		example:   "  ankra ai memories profile reset --yes",
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			if confirmError := confirmOrganisationChange(command,
				"Resetting the learned profile clears what the assistant has learned about your preferences."); confirmError != nil {
				return nil, confirmError
			}
			if resetError := apiClient.ResetAIMemoryProfile(command.Context()); resetError != nil {
				return nil, resetError
			}
			return acknowledgementDocument(map[string]any{"reset": true})
		},
		human: func(out io.Writer, _ json.RawMessage) error {
			_, _ = fmt.Fprintln(out, "Learned profile reset.")
			return nil
		},
	})
	resetCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	profileCommand.AddCommand(getCommand, setCommand, resetCommand)
	return profileCommand
}

func newApplicationAutonomyActionsCommand() *cobra.Command {
	return newOperationCommand(operationCommand{
		use:   "actions",
		short: "List what Ankra did on its own across every application",
		long: `List what Ankra did on its own across every application, newest first (at
most 20): auto-remediation runs on an application's placements, Cost Autopilot
actions the autopilot approved itself, and build fixes the automatic lane
sent. Each action names its applications, the setting that allowed it and
whether it can be undone. A source with more actions than it returned, or one
that could not be read, is named.`,
		example:   "  ankra application autonomy actions\n  ankra application autonomy actions -o json",
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			return apiClient.ListApplicationAutonomousActions(command.Context())
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var listing struct {
				Actions []struct {
					Kind         string  `json:"kind"`
					Status       string  `json:"status"`
					OccurredAt   string  `json:"occurred_at"`
					Rule         *string `json:"rule"`
					Applications []struct {
						Name string `json:"name"`
					} `json:"applications"`
					Undo *struct {
						Available bool `json:"available"`
					} `json:"undo"`
				} `json:"actions"`
				CappedSources      []string `json:"capped_sources"`
				UnavailableSources []string `json:"unavailable_sources"`
			}
			if decodeError := decodeDocument(document, &listing); decodeError != nil {
				return decodeError
			}
			if len(listing.Actions) == 0 {
				_, _ = fmt.Fprintln(out, "Ankra took no action on its own.")
			} else {
				rows := make([]table.Row, 0, len(listing.Actions))
				for _, action := range listing.Actions {
					names := make([]string, 0, len(action.Applications))
					for _, application := range action.Applications {
						names = append(names, application.Name)
					}
					undo := "-"
					if action.Undo != nil {
						undo = yesNo(action.Undo.Available)
					}
					rows = append(rows, table.Row{formatTimeAgo(action.OccurredAt), action.Kind, action.Status,
						orDash(strings.Join(names, ", ")), valueOrDash(action.Rule), undo})
				}
				renderRoundedTable(out, table.Row{"When", "Kind", "Status", "Applications", "Allowed by", "Can undo"}, rows)
			}
			if len(listing.CappedSources) > 0 {
				_, _ = fmt.Fprintf(out, "More actions exist than are listed for: %s.\n", strings.Join(listing.CappedSources, ", "))
			}
			if len(listing.UnavailableSources) > 0 {
				_, _ = fmt.Fprintf(out, "Could not be read, so the list may be incomplete: %s.\n", strings.Join(listing.UnavailableSources, ", "))
			}
			return nil
		},
	})
}

func init() {
	credentialsCmd.AddCommand(newCredentialsPropagationCommand())
	aiMemoriesCmd.AddCommand(newAIMemoriesProfileCommand())
}
