package cmd

// How much of an application Ankra runs unasked: the "who runs it" level of
// each environment, the pause a person puts on its autonomous actions, and
// the two unattended lanes with a switch of their own - fixing failed builds
// and merging setup pull requests.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

const applicationAutonomyLevels = "you_run_it, together or ankra_runs_it"

type applicationAutonomyRead struct {
	ReadState    string `json:"read_state"`
	Environments []struct {
		EnvironmentID   string `json:"environment_id"`
		Name            string `json:"name"`
		EnvironmentKind string `json:"environment_kind"`
		Level           string `json:"level"`
		Areas           []struct {
			Area  string `json:"area"`
			Level string `json:"level"`
		} `json:"areas"`
	} `json:"environments"`
	Layers *struct {
		Application  map[string]any            `json:"application"`
		Environments map[string]map[string]any `json:"environments"`
	} `json:"layers"`
	Pause struct {
		Paused *bool   `json:"paused"`
		Reason *string `json:"reason"`
	} `json:"pause"`
}

func newApplicationAutonomyCommand() *cobra.Command {
	autonomyCommand := &cobra.Command{
		Use:     "autonomy",
		Aliases: []string{"who-runs-it"},
		Short:   "Inspect and set how much of an application Ankra runs on its own",
		Long: `Inspect and set who runs an application: for each of its environments, in
each area (keep_it_up, ship_changes, keep_it_safe, keep_costs_down,
keep_it_current, fix_my_code), whether you run it, you and Ankra run it
together, or Ankra runs it.

'pause' stops Ankra's autonomous actions for the application without changing
any level; 'resume' lifts it. The organisation-wide stop is
'ankra ai autonomy'.`,
	}
	autonomyCommand.AddCommand(
		newApplicationAutonomyGetCommand(),
		newApplicationAutonomySetCommand(),
		newApplicationAutonomyPauseCommand(),
		newApplicationAutonomyActionsCommand(),
		newApplicationDocumentCommand("resume", "Let Ankra act on an application again",
			"Lift the application's pause, so what Ankra does unasked on its placements runs\nagain under the application's own autonomy setting.",
			func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
				return apiClient.ResumeApplicationAutonomy(command.Context(), applicationID)
			}, printApplicationAutonomyPause),
		newApplicationDocumentCommand("pause-status", "Show whether Ankra is paused for an application",
			"Show whether a person paused Ankra's autonomous actions for the application,\nwho did and why.",
			func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
				return apiClient.GetApplicationAutonomyPause(command.Context(), applicationID)
			}, printApplicationAutonomyPause),
	)
	return autonomyCommand
}

// newApplicationDocumentCommand builds a `<verb> <application-id>` command
// that sends one request for the application and renders its answer.
func newApplicationDocumentCommand(name string, short string, long string,
	invoke applicationSubresourceInvoke, human func(out io.Writer, document json.RawMessage) error) *cobra.Command {
	documentCommand := &cobra.Command{
		Use:   name + " <application-id>",
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			document, invokeError := invoke(command, applicationID)
			if invokeError != nil {
				return invokeError
			}
			return renderDocument(command, document, func(out io.Writer) error { return human(out, document) })
		},
	}
	registerStructuredOutputFlags(documentCommand)
	return documentCommand
}

func printApplicationAutonomyPause(out io.Writer, document json.RawMessage) error {
	var pause struct {
		Paused   bool    `json:"paused"`
		PausedAt *string `json:"paused_at"`
		PausedBy *string `json:"paused_by"`
		Reason   *string `json:"reason"`
	}
	if decodeError := decodeDocument(document, &pause); decodeError != nil {
		return decodeError
	}
	if !pause.Paused {
		_, _ = fmt.Fprintln(out, "Ankra is not paused for this application.")
		return nil
	}
	_, _ = fmt.Fprintln(out, "Ankra is PAUSED for this application: auto-remediation and Cost Autopilot do not act on it.")
	_, _ = fmt.Fprintf(out, "Paused at: %s\n", valueOrDash(pause.PausedAt))
	_, _ = fmt.Fprintf(out, "Paused by: %s\n", valueOrDash(pause.PausedBy))
	_, _ = fmt.Fprintf(out, "Reason:    %s\n", valueOrDash(pause.Reason))
	return nil
}

func newApplicationAutonomyGetCommand() *cobra.Command {
	return newApplicationDocumentCommand("get", "Show who runs each of an application's environments",
		`Show the effective level of every environment and the areas that differ from
it. -o json carries the whole answer: the rule that decided each area, the
limits, the stored layers a 'set' starts from, and the application-wide gate.`,
		func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationAutonomy(command.Context(), applicationID)
		},
		func(out io.Writer, document json.RawMessage) error {
			var autonomy applicationAutonomyRead
			if decodeError := decodeDocument(document, &autonomy); decodeError != nil {
				return decodeError
			}
			writer := table.NewWriter()
			writer.SetOutputMirror(out)
			writer.AppendHeader(table.Row{"ENVIRONMENT", "ID", "KIND", "LEVEL", "AREAS THAT DIFFER"})
			for _, environment := range autonomy.Environments {
				differing := []string{}
				for _, area := range environment.Areas {
					if area.Level != environment.Level {
						differing = append(differing, area.Area+"="+area.Level)
					}
				}
				writer.AppendRow(table.Row{environment.Name, environment.EnvironmentID, environment.EnvironmentKind,
					environment.Level, orDash(strings.Join(differing, ", "))})
			}
			writer.Render()
			if autonomy.ReadState != "" && autonomy.ReadState != "ok" {
				_, _ = fmt.Fprintf(out, "This read is %s: some of the settings behind it could not be read.\n", autonomy.ReadState)
			}
			switch {
			case autonomy.Pause.Paused == nil:
				_, _ = fmt.Fprintln(out, "Whether Ankra is paused for this application could not be read.")
			case *autonomy.Pause.Paused:
				_, _ = fmt.Fprintf(out, "Ankra is PAUSED for this application (%s).\n", valueOrDash(autonomy.Pause.Reason))
			}
			return nil
		})
}

// applicationAutonomyLayer builds the whole layer a set writes: what is
// stored for the scope, with the -f document and then the flags laid over it.
func applicationAutonomyLayer(command *cobra.Command, stored map[string]any) (map[string]any, error) {
	layer := map[string]any{"level": stored["level"]}
	for _, member := range []string{"areas", "limits"} {
		entries, _ := stored[member].(map[string]any)
		copied := make(map[string]any, len(entries))
		for name, value := range entries {
			copied[name] = value
		}
		layer[member] = copied
	}
	if path, _ := command.Flags().GetString("file"); path != "" {
		document, readError := readAlertRuleDocument(command)
		if readError != nil {
			return nil, readError
		}
		for _, member := range []string{"level", "areas", "limits"} {
			if value, isNamed := document[member]; isNamed {
				layer[member] = value
			}
		}
	}
	if command.Flags().Changed("level") {
		level, _ := command.Flags().GetString("level")
		layer["level"] = nil
		if level = strings.TrimSpace(level); level != "" && level != "none" {
			layer["level"] = level
		}
	}
	areas, _ := layer["areas"].(map[string]any)
	limits, _ := layer["limits"].(map[string]any)
	if areas == nil || limits == nil {
		return nil, withExitCode(exitUsage, errors.New("areas and limits must each be an object"))
	}
	areaFlags, _ := command.Flags().GetStringArray("area")
	for _, areaFlag := range areaFlags {
		area, level, isPair := strings.Cut(areaFlag, "=")
		if !isPair || strings.TrimSpace(area) == "" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--area %q must be <area>=<level>, or <area>= to drop the area's own level", areaFlag))
		}
		if level = strings.TrimSpace(level); level == "" {
			delete(areas, strings.TrimSpace(area))
			continue
		}
		areas[strings.TrimSpace(area)] = level
	}
	for _, limit := range []string{"monthly-cost-increase-cents", "production-downtime-seconds", "non-production-downtime-seconds"} {
		if command.Flags().Changed(limit) {
			value, _ := command.Flags().GetInt(limit)
			if value < 0 {
				return nil, withExitCode(exitUsage, fmt.Errorf("--%s must not be negative", limit))
			}
			limits[strings.ReplaceAll(limit, "-", "_")] = value
		}
	}
	if command.Flags().Changed("may-change-data") {
		limits["may_change_data"], _ = command.Flags().GetBool("may-change-data")
	}
	return layer, nil
}

func newApplicationAutonomySetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application-id>",
		Short: "Set who runs an application, or one of its environments",
		Long: `Store the application's own "who runs it" layer, or with --environment one
environment's, which applies over the application's.

The platform stores a layer whole. This command starts from the layer that is
stored and changes only what you name, as flags or as the level, areas and
limits members of -f (YAML or JSON, - reads stdin), and sends the layer it
read along as the expected state: if someone changed it in between, the
platform answers 409 and nothing is written.

Anyone who may write the application may set anything within what the
organisation allows; asking for more needs an organisation admin. Confirms
first; --yes skips the prompt.`,
		Example: `  ankra application autonomy set shop --level together
  ankra application autonomy set shop --environment production --area ship_changes=you_run_it
  ankra application autonomy set shop --area keep_costs_down=ankra_runs_it --monthly-cost-increase-cents 5000`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			namesAChange := false
			for _, flagName := range []string{"level", "area", "monthly-cost-increase-cents", "production-downtime-seconds",
				"non-production-downtime-seconds", "may-change-data", "file"} {
				namesAChange = namesAChange || command.Flags().Changed(flagName)
			}
			if !namesAChange {
				return withExitCode(exitUsage, errors.New("nothing to set: pass --level, --area, a limit or -f"))
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			current, readError := apiClient.GetApplicationAutonomy(command.Context(), applicationID)
			if readError != nil {
				return readError
			}
			var autonomy applicationAutonomyRead
			if decodeError := decodeDocument(current, &autonomy); decodeError != nil {
				return decodeError
			}
			if autonomy.Layers == nil {
				return errors.New("the platform could not read the stored autonomy layers, so no layer can be built from them; nothing was written")
			}
			stored := autonomy.Layers.Application
			scope := "application " + strings.TrimSpace(arguments[0])
			environmentID := ""
			if environmentReference, _ := command.Flags().GetString("environment"); strings.TrimSpace(environmentReference) != "" {
				environmentReference = strings.TrimSpace(environmentReference)
				known := []string{}
				named := []string{}
				for _, environment := range autonomy.Environments {
					known = append(known, environment.Name)
					if environment.EnvironmentID == environmentReference {
						environmentID = environment.EnvironmentID
					}
					if environment.Name == environmentReference {
						named = append(named, environment.EnvironmentID)
					}
				}
				if environmentID == "" && len(named) > 1 {
					return withExitCode(exitUsage, fmt.Errorf("%d environments are named %q - pass the environment id instead (%s)",
						len(named), environmentReference, strings.Join(named, ", ")))
				}
				if environmentID == "" && len(named) == 1 {
					environmentID = named[0]
				}
				if environmentID == "" {
					return withExitCode(exitUsage, fmt.Errorf("the application has no environment %q; it has: %s",
						environmentReference, strings.Join(known, ", ")))
				}
				stored = autonomy.Layers.Environments[environmentID]
				scope = "environment " + environmentReference + " of " + scope
			}
			layer, layerError := applicationAutonomyLayer(command, stored)
			if layerError != nil {
				return layerError
			}
			// A nil map is sent as null, which is how the platform is told
			// that no layer was stored when this one was built.
			layer["expected"] = stored
			skipConfirmation, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Set who runs %s to %s? [y/N]: ", scope, describeApplicationAutonomyLayer(layer)),
				skipConfirmation); confirmError != nil {
				return confirmError
			}
			document, setError := apiClient.SetApplicationAutonomy(command.Context(), applicationID, environmentID, layer)
			if setError != nil {
				return setError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var written struct {
					WidenedByAdmin bool `json:"widened_by_admin"`
				}
				if decodeError := decodeDocument(document, &written); decodeError != nil {
					return decodeError
				}
				_, _ = fmt.Fprintf(out, "Stored for %s: %s.\n", scope, describeApplicationAutonomyLayer(layer))
				if written.WidenedByAdmin {
					_, _ = fmt.Fprintln(out, "This asks for more than the layer inherits and is recorded as widened by an admin.")
				}
				return nil
			})
		},
	}
	setCommand.Flags().String("environment", "", "Environment name or id whose layer to set, instead of the application's")
	setCommand.Flags().String("level", "", "Level for every area without its own: "+applicationAutonomyLevels+"; none clears it")
	setCommand.Flags().StringArray("area", nil, "One area's level as <area>=<level>; <area>= drops it; repeatable")
	setCommand.Flags().Int("monthly-cost-increase-cents", 0, "Most a change Ankra makes alone may add to the monthly cost")
	setCommand.Flags().Int("production-downtime-seconds", 0, "Most production downtime a change Ankra makes alone may cause")
	setCommand.Flags().Int("non-production-downtime-seconds", 0, "Most non-production downtime a change Ankra makes alone may cause")
	setCommand.Flags().Bool("may-change-data", false, "Whether a change Ankra makes alone may change data")
	setCommand.Flags().StringP("file", "f", "", "YAML or JSON file with the layer's level, areas and limits (- reads stdin)")
	setCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

// describeApplicationAutonomyLayer says a layer in one line for the prompt
// and the confirmation of what was stored.
func describeApplicationAutonomyLayer(layer map[string]any) string {
	parts := []string{"level none"}
	if level, _ := layer["level"].(string); level != "" {
		parts[0] = "level " + level
	}
	for _, member := range []string{"areas", "limits"} {
		entries, _ := layer[member].(map[string]any)
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("%s=%v", name, entries[name]))
		}
	}
	return strings.Join(parts, ", ")
}

func newApplicationAutonomyPauseCommand() *cobra.Command {
	pauseCommand := &cobra.Command{
		Use:   "pause <application-id>",
		Short: "Pause Ankra's autonomous actions for an application",
		Long: `Pause what Ankra does unasked for this application. From the next dispatch
auto-remediation does not act on its namespaces and Cost Autopilot does not
file or run changes on its clusters; a remediation run already acting there
stops before its next mutating step. Chat and read-only investigation keep
working. 'ankra application autonomy resume' lifts it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			reason, _ := command.Flags().GetString("reason")
			document, pauseError := apiClient.PauseApplicationAutonomy(command.Context(), applicationID, strings.TrimSpace(reason))
			if pauseError != nil {
				return pauseError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				return printApplicationAutonomyPause(out, document)
			})
		},
	}
	pauseCommand.Flags().String("reason", "", "Why the application is paused, shown to whoever reads its pause")
	registerStructuredOutputFlags(pauseCommand)
	return pauseCommand
}

func printApplicationSwitch(label string) func(out io.Writer, document json.RawMessage) error {
	return func(out io.Writer, document json.RawMessage) error {
		var setting struct {
			Enabled             *bool `json:"enabled"`
			OrganisationEnabled *bool `json:"organisation_enabled"`
			Effective           *bool `json:"effective"`
		}
		if decodeError := decodeDocument(document, &setting); decodeError != nil {
			return decodeError
		}
		state := "could not be read"
		switch {
		case setting.Enabled != nil:
			state = map[bool]string{true: "on", false: "off"}[*setting.Enabled]
		case setting.OrganisationEnabled != nil:
			state = "follows the organisation"
		}
		_, _ = fmt.Fprintf(out, "%s: %s\n", label, state)
		if setting.OrganisationEnabled != nil && setting.Effective != nil {
			_, _ = fmt.Fprintf(out, "Organisation setting: %s; in effect: %s\n",
				strconv.FormatBool(*setting.OrganisationEnabled), strconv.FormatBool(*setting.Effective))
		}
		return nil
	}
}

// newApplicationSwitchCommand builds the get|set pair of an application
// switch. followsOrganisation adds --follow-organisation, for a switch whose
// application answer can be cleared.
func newApplicationSwitchCommand(use string, short string, long string, followsOrganisation bool,
	human func(out io.Writer, document json.RawMessage) error, read applicationSubresourceInvoke,
	write func(command *cobra.Command, applicationID string, enabled *bool) (json.RawMessage, error)) *cobra.Command {
	switchCommand := &cobra.Command{Use: use, Short: short, Long: long}
	setCommand := &cobra.Command{
		Use:   "set <application-id>",
		Short: "Turn it on or off",
		Long: `Turn it on or off. --enabled must be given explicitly: on and off are both
deliberate acts, and neither is a safe default to infer from a bare 'set'.`,
		Example: fmt.Sprintf("  ankra application %s set <application-id> --enabled\n  ankra application %s set <application-id> --enabled=false", use, use),
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			followOrganisation := false
			if followsOrganisation {
				followOrganisation, _ = command.Flags().GetBool("follow-organisation")
			}
			if command.Flags().Changed("enabled") == followOrganisation {
				if followsOrganisation {
					return withExitCode(exitUsage, errors.New("pass exactly one of --enabled, --enabled=false or --follow-organisation"))
				}
				return withExitCode(exitUsage, errors.New("--enabled is required: pass --enabled to turn it on, or --enabled=false to turn it off"))
			}
			var enabled *bool
			if !followOrganisation {
				value, _ := command.Flags().GetBool("enabled")
				enabled = &value
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			document, writeError := write(command, applicationID, enabled)
			if writeError != nil {
				return writeError
			}
			return renderDocument(command, document, func(out io.Writer) error { return human(out, document) })
		},
	}
	setCommand.Flags().Bool("enabled", false, "Turn it on (--enabled) or off (--enabled=false)")
	if followsOrganisation {
		setCommand.Flags().Bool("follow-organisation", false, "Clear the application's own answer so it follows its organisation")
	}
	registerStructuredOutputFlags(setCommand)
	switchCommand.AddCommand(setCommand)
	if read != nil {
		switchCommand.AddCommand(newApplicationDocumentCommand("get", "Show the setting", "", read, human))
	}
	return switchCommand
}

func newApplicationAutoFixBuildsCommand() *cobra.Command {
	return newApplicationSwitchCommand("auto-fix-builds",
		"Inspect and set whether Ankra fixes an application's failed builds on its own",
		`Inspect and set whether Ankra runs its fix-build lane when a build of the
application's tracked branch fails: deterministic repairs first, then an agent
that opens a pull request. It is on by default and bounded to one run per
application per day; the switch exists to opt out, and is read fresh on every
failed build.`,
		false, printApplicationSwitch("Automatic build fixing"),
		func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationAutoFixBuilds(command.Context(), applicationID)
		},
		func(command *cobra.Command, applicationID string, enabled *bool) (json.RawMessage, error) {
			return apiClient.SetApplicationAutoFixBuilds(command.Context(), applicationID, *enabled)
		})
}

func newApplicationSetupAutoMergeCommand() *cobra.Command {
	return newApplicationSwitchCommand("setup-auto-merge",
		"Inspect and set whether Ankra merges an application's setup pull requests on its own",
		`Inspect and set whether Ankra may merge this application's setup pull requests
on its own once every check and Ankra's proof of the head are green.

The application's own answer wins over its organisation's either way;
--follow-organisation clears it. Setting it needs an organisation admin.`,
		true, printApplicationSwitch("Setup pull request auto-merge"),
		func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationSetupAutoMerge(command.Context(), applicationID)
		},
		func(command *cobra.Command, applicationID string, enabled *bool) (json.RawMessage, error) {
			return apiClient.SetApplicationSetupAutoMerge(command.Context(), applicationID, enabled)
		})
}

func newApplicationDatabaseBackupCommand() *cobra.Command {
	return newApplicationSwitchCommand("database-backup",
		"Set whether an application's database is backed up by default",
		`Set the application's database backup default: whether a deployment of it
that carries a database is protected without anyone asking. 'ankra application
backups' reads it, as database_backup, beside each deployment's protection.
Needs backups.manage.`,
		false,
		func(out io.Writer, document json.RawMessage) error {
			var backups struct {
				DatabaseBackup *bool `json:"database_backup"`
			}
			if decodeError := decodeDocument(document, &backups); decodeError != nil {
				return decodeError
			}
			if backups.DatabaseBackup == nil {
				return errors.New("the platform answered without the database backup default")
			}
			_, _ = fmt.Fprintf(out, "Database backup default: %s\n", map[bool]string{true: "on", false: "off"}[*backups.DatabaseBackup])
			return nil
		}, nil,
		func(command *cobra.Command, applicationID string, enabled *bool) (json.RawMessage, error) {
			return apiClient.SetApplicationDatabaseBackup(command.Context(), applicationID, *enabled)
		})
}

func newApplicationCostEstimateCommand() *cobra.Command {
	estimateCommand := &cobra.Command{
		Use:   "cost-estimate <application-id>",
		Short: "Estimate what a change to an application would cost a month",
		Long: `Price a proposed change to an application as the monthly change to its cost,
before making it. --change names the kind:

  replicas         a deployment's replica count (--replicas)
  resources        its per-pod requests (--cpu-request, --memory-request)
  park             parking the deployment
  promote          promoting a build into it
  new_environment  a new environment on --cluster
  database         a database preset on --cluster (--service, --parameter)

--cluster and --namespace name the deployment, or for new_environment and
database the cluster the workload would run on. A change that cannot be priced
says why instead of answering zero.`,
		Example: `  ankra application cost-estimate shop --change replicas --cluster production --namespace shop --replicas 4
  ankra application cost-estimate shop --change database --cluster production --parameter instances=2`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			change, _ := command.Flags().GetString("change")
			if strings.TrimSpace(change) == "" {
				return withExitCode(exitUsage, errors.New(
					"--change is required: replicas, resources, park, promote, new_environment or database"))
			}
			estimate := map[string]any{"change": strings.TrimSpace(change)}
			for flagName, member := range map[string]string{
				"namespace": "namespace", "cpu-request": "cpu_request", "memory-request": "memory_request", "service": "service",
			} {
				if value, _ := command.Flags().GetString(flagName); strings.TrimSpace(value) != "" {
					estimate[member] = strings.TrimSpace(value)
				}
			}
			if command.Flags().Changed("replicas") {
				estimate["replicas"], _ = command.Flags().GetInt("replicas")
			}
			parameterFlags, _ := command.Flags().GetStringArray("parameter")
			if len(parameterFlags) > 0 {
				parameters := map[string]int{}
				for _, parameterFlag := range parameterFlags {
					name, rawValue, _ := strings.Cut(parameterFlag, "=")
					value, parseError := strconv.Atoi(strings.TrimSpace(rawValue))
					if strings.TrimSpace(name) == "" || parseError != nil {
						return withExitCode(exitUsage, fmt.Errorf("--parameter %q must be <name>=<integer>", parameterFlag))
					}
					parameters[strings.TrimSpace(name)] = value
				}
				estimate["parameters"] = parameters
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			if clusterReference, _ := command.Flags().GetString("cluster"); strings.TrimSpace(clusterReference) != "" {
				clusterID, clusterError := resolveClusterID(strings.TrimSpace(clusterReference))
				if clusterError != nil {
					return clusterError
				}
				estimate["cluster_id"] = clusterID
			}
			document, estimateError := apiClient.EstimateApplicationCost(command.Context(), applicationID, estimate)
			if estimateError != nil {
				return estimateError
			}
			return renderDocument(command, document, func(out io.Writer) error { return printApplicationCostEstimate(out, document) })
		},
	}
	estimateCommand.Flags().String("change", "", "Kind of change: replicas, resources, park, promote, new_environment or database (required)")
	estimateCommand.Flags().String("cluster", "", "Cluster name or id the deployment runs on, or the new workload would")
	estimateCommand.Flags().String("namespace", "", "Namespace the deployment runs in")
	estimateCommand.Flags().Int("replicas", 0, "Proposed replica count")
	estimateCommand.Flags().String("cpu-request", "", "Proposed CPU request per pod, such as 500m")
	estimateCommand.Flags().String("memory-request", "", "Proposed memory request per pod, such as 512Mi")
	estimateCommand.Flags().String("service", "", "Managed service engine of a database preset (default service-postgresql)")
	estimateCommand.Flags().StringArray("parameter", nil, "A database preset's plan parameter as <name>=<integer>; repeatable")
	registerStructuredOutputFlags(estimateCommand)
	return estimateCommand
}

func printApplicationCostEstimate(out io.Writer, document json.RawMessage) error {
	var estimate struct {
		Currency            string  `json:"currency"`
		Priced              bool    `json:"priced"`
		UnpricedReason      *string `json:"unpriced_reason"`
		MonthlyDeltaCents   *int64  `json:"monthly_delta_cents"`
		CurrentMonthlyCents *int64  `json:"current_monthly_cents"`
		Confidence          *string `json:"confidence"`
		CoverageIncomplete  *bool   `json:"coverage_incomplete"`
		Basis               *string `json:"basis"`
	}
	if decodeError := decodeDocument(document, &estimate); decodeError != nil {
		return decodeError
	}
	currency := strings.ToUpper(estimate.Currency)
	if currency == "" {
		currency = "(currency not named)"
	}
	if !estimate.Priced || estimate.MonthlyDeltaCents == nil {
		_, _ = fmt.Fprintf(out, "This change could not be priced: %s\n", valueOrDash(estimate.UnpricedReason))
		return nil
	}
	_, _ = fmt.Fprintf(out, "Monthly change:  %+.2f %s\n", float64(*estimate.MonthlyDeltaCents)/100, currency)
	if estimate.CurrentMonthlyCents != nil {
		_, _ = fmt.Fprintf(out, "Costs today:     %.2f %s a month\n", float64(*estimate.CurrentMonthlyCents)/100, currency)
	}
	_, _ = fmt.Fprintf(out, "Confidence:      %s\n", valueOrDash(estimate.Confidence))
	if estimate.CoverageIncomplete != nil && *estimate.CoverageIncomplete {
		_, _ = fmt.Fprintln(out, "This is a floor: something the change would use is not priced.")
	}
	if estimate.Basis != nil && *estimate.Basis != "" {
		_, _ = fmt.Fprintf(out, "Basis:           %s\n", *estimate.Basis)
	}
	return nil
}

func registerApplicationAutonomyCommands(applicationCommand *cobra.Command) {
	applicationCommand.AddCommand(
		newApplicationAutonomyCommand(),
		newApplicationAutoFixBuildsCommand(),
		newApplicationSetupAutoMergeCommand(),
		newApplicationDatabaseBackupCommand(),
		newApplicationCostEstimateCommand(),
	)
}
