package cmd

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// Draft parameters travel as generic maps (client.StackProfileDraft) so an
// edit never drops a field this client does not model. The helpers below
// read and write only the members the option commands touch.

var parameterTypeChoices = []string{"string", "number", "boolean", "enum"}

func draftParameterByName(parameters []map[string]any, name string) map[string]any {
	for _, parameter := range parameters {
		if parameterName, _ := parameter["name"].(string); parameterName == name {
			return parameter
		}
	}
	return nil
}

func draftParameterNames(parameters []map[string]any) []string {
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if name, _ := parameter["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func draftParameterOptions(parameter map[string]any) []map[string]any {
	raw, _ := parameter["options"].([]any)
	options := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if option, ok := item.(map[string]any); ok {
			options = append(options, option)
		}
	}
	return options
}

// storeDraftParameterOptions writes the options back and keeps the enum
// mirror the platform maintains (an input with options is an enum over its
// option values), so a reader that predates options still sees the choices.
func storeDraftParameterOptions(parameter map[string]any, options []map[string]any) {
	if len(options) == 0 {
		delete(parameter, "options")
		return
	}
	stored := make([]any, 0, len(options))
	enumValues := make([]any, 0, len(options))
	for _, option := range options {
		stored = append(stored, option)
		enumValues = append(enumValues, fmt.Sprint(option["value"]))
	}
	parameter["options"] = stored
	parameter["type"] = "enum"
	parameter["enum_values"] = enumValues
}

func draftOptionSets(option map[string]any) map[string]string {
	raw, _ := option["sets"].(map[string]any)
	sets := map[string]string{}
	for target, value := range raw {
		sets[target] = fmt.Sprint(value)
	}
	return sets
}

func storeDraftOptionSets(option map[string]any, sets map[string]string) {
	if len(sets) == 0 {
		delete(option, "sets")
		return
	}
	stored := map[string]any{}
	for target, value := range sets {
		stored[target] = value
	}
	option["sets"] = stored
}

func sortedAssignmentTargets(sets map[string]string) []string {
	targets := make([]string, 0, len(sets))
	for target := range sets {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

// describeAssignmentProblem mirrors the rules publish enforces on an
// option's targets, so the author hears about a bad target now rather than
// at publish. The server stays authoritative.
func describeAssignmentProblem(parameters []map[string]any, selectorName string, target string) string {
	if target == selectorName {
		return fmt.Sprintf("an option of %q cannot set %q itself", selectorName, target)
	}
	targetParameter := draftParameterByName(parameters, target)
	if targetParameter == nil {
		return fmt.Sprintf("%q is not an input on this draft; it has: %v", target, draftParameterNames(parameters))
	}
	if targetType, _ := targetParameter["type"].(string); targetType == "secret" {
		return fmt.Sprintf("%q is a secret input; secret values are never stored in a profile", target)
	}
	if len(draftParameterOptions(targetParameter)) > 0 {
		return fmt.Sprintf("%q offers options of its own; options cannot drive other options", target)
	}
	return ""
}

// describeDraftParameterChoices renders an input's options on one line for
// `drafts get`, e.g. "8b (sets model_id=Qwen/Qwen3-8B), 32b (sets ...)".
func describeDraftParameterChoices(parameter map[string]any) string {
	options := draftParameterOptions(parameter)
	if len(options) == 0 {
		return ""
	}
	parts := make([]string, 0, len(options))
	for _, option := range options {
		sets := draftOptionSets(option)
		assignments := make([]string, 0, len(sets))
		for _, target := range sortedAssignmentTargets(sets) {
			assignments = append(assignments, target+"="+sets[target])
		}
		part := fmt.Sprint(option["value"])
		if len(assignments) > 0 {
			part += " (sets " + strings.Join(assignments, ", ") + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func splitEnumList(raw string) []string {
	values := []string{}
	for _, value := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func writeDraftParameters(draft *client.StackProfileDraft) (*client.StackProfileDraft, error) {
	updated, err := apiClient.UpdateStackProfileDraft(draft.ID, client.UpdateStackProfileDraftRequest{
		Spec:       draft.Spec,
		Parameters: draft.Parameters,
		Version:    draft.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("updating stack profile draft: %w", err)
	}
	return updated, nil
}

var stackProfileDraftsOptionsCmd = &cobra.Command{
	Use:   "options",
	Short: "Give an input choices that set other inputs",
	Long: `Give a draft's input choices, where each choice also answers other inputs.

One "Model size" input with a choice of 8b or 32b can then move the model id,
the context length and the volume size together, so whoever applies the
profile picks one thing instead of keeping four consistent. At apply time the
platform resolves a choice in a fixed order: a value passed with --set, then
what the selected choice sets, then the input's own default.

A choice can set any non-secret input that does not offer choices of its own.
Declare a choice input that no manifest references with
'drafts annotate <draft-id> --parameter <name> --add --type enum'.`,
}

var stackProfileDraftsOptionsSetCmd = &cobra.Command{
	Use:   "set <draft-id>",
	Short: "Add or update one choice on an input",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		parameterName, _ := cmd.Flags().GetString("parameter")
		value, _ := cmd.Flags().GetString("value")
		title, _ := cmd.Flags().GetString("title")
		description, _ := cmd.Flags().GetString("description")
		assignments, _ := cmd.Flags().GetStringArray("set")
		removals, _ := cmd.Flags().GetStringArray("unset")
		value = strings.TrimSpace(value)
		if value == "" {
			return withExitCode(exitUsage, errors.New("--value is required: the value the input takes when this choice is picked"))
		}

		draft, err := apiClient.GetStackProfileDraft(args[0])
		if err != nil {
			return fmt.Errorf("reading stack profile draft: %w", err)
		}
		parameter := draftParameterByName(draft.Parameters, parameterName)
		declared := false
		if parameter == nil {
			// An input no manifest references is kept by the platform only
			// while it offers choices, so the first choice is what declares
			// it; there is nothing to declare separately first.
			parameter = map[string]any{
				"name": parameterName, "title": parameterName, "type": "enum",
				"required": false, "enum_values": []any{}, "group": "variables",
			}
			draft.Parameters = append(draft.Parameters, parameter)
			declared = true
		}
		if parameterType, _ := parameter["type"].(string); parameterType == "secret" {
			return withExitCode(exitUsage, fmt.Errorf("%q is a secret input and cannot offer choices", parameterName))
		}

		options := draftParameterOptions(parameter)
		var option map[string]any
		for _, candidate := range options {
			if fmt.Sprint(candidate["value"]) == value {
				option = candidate
				break
			}
		}
		created := option == nil
		if created {
			option = map[string]any{"value": value}
			options = append(options, option)
		}
		if title != "" {
			option["title"] = title
		}
		if description != "" {
			option["description"] = description
		}
		sets := draftOptionSets(option)
		for _, entry := range assignments {
			target, assigned, splitError := splitParameterAssignment(entry, "--set")
			if splitError != nil {
				return withExitCode(exitUsage, splitError)
			}
			if problem := describeAssignmentProblem(draft.Parameters, parameterName, target); problem != "" {
				return withExitCode(exitUsage, errors.New(problem))
			}
			sets[target] = assigned
		}
		for _, target := range removals {
			delete(sets, strings.TrimSpace(target))
		}
		storeDraftOptionSets(option, sets)
		storeDraftParameterOptions(parameter, options)

		updated, err := writeDraftParameters(draft)
		if err != nil {
			return err
		}
		if handled, err := renderStructured(cmd, updated); err != nil {
			return err
		} else if handled {
			return nil
		}
		verb := "Updated"
		if created {
			verb = "Added"
		}
		if declared {
			fmt.Printf("Declared input %s on draft '%s'.\n", parameterName, updated.Name)
		}
		fmt.Printf("%s choice '%s' on %s (%d %s).\n", verb, value, parameterName, len(options), pluralise(len(options), "choice", "choices"))
		for _, target := range sortedAssignmentTargets(sets) {
			fmt.Printf("  sets %s=%s\n", target, sets[target])
		}
		if len(sets) == 0 {
			fmt.Println("  sets nothing yet: add --set <input>=<value> to have this choice answer other inputs")
		}
		fmt.Println("Publish to make it live:")
		fmt.Printf("  ankra stack-profiles drafts publish %s --changelog \"...\"\n", updated.ID)
		return nil
	},
}

var stackProfileDraftsOptionsRemoveCmd = &cobra.Command{
	Use:   "remove <draft-id>",
	Short: "Remove one choice from an input",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		parameterName, _ := cmd.Flags().GetString("parameter")
		value, _ := cmd.Flags().GetString("value")
		value = strings.TrimSpace(value)
		if value == "" {
			return withExitCode(exitUsage, errors.New("--value is required"))
		}

		draft, err := apiClient.GetStackProfileDraft(args[0])
		if err != nil {
			return fmt.Errorf("reading stack profile draft: %w", err)
		}
		parameter := draftParameterByName(draft.Parameters, parameterName)
		if parameter == nil {
			return fmt.Errorf("parameter %q not found on the draft; it has: %v", parameterName, draftParameterNames(draft.Parameters))
		}
		options := draftParameterOptions(parameter)
		remaining := make([]map[string]any, 0, len(options))
		for _, option := range options {
			if fmt.Sprint(option["value"]) != value {
				remaining = append(remaining, option)
			}
		}
		if len(remaining) == len(options) {
			values := make([]string, 0, len(options))
			for _, option := range options {
				values = append(values, fmt.Sprint(option["value"]))
			}
			return fmt.Errorf("%q has no choice %q; it has: %v", parameterName, value, values)
		}
		storeDraftParameterOptions(parameter, remaining)

		updated, err := writeDraftParameters(draft)
		if err != nil {
			return err
		}
		if handled, err := renderStructured(cmd, updated); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Removed choice '%s' from %s (%d %s left).\n", value, parameterName, len(remaining), pluralise(len(remaining), "choice", "choices"))
		return nil
	},
}

func pluralise(count int, singular string, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

// bindingPreview is one row of `apply --dry-run`: the value an input will
// deploy with and where it came from.
type bindingPreview struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Source   string `json:"source"`
	Required bool   `json:"required"`
}

// previewResolvedBindings applies the platform's resolution order - a value
// you set, then what the selected choice sets, then the input's default - to
// the bindings without creating anything, so the whole set can be read before
// apply. Later choice inputs win on overlap, as on the server. Secret values
// are never echoed.
func previewResolvedBindings(parameters []client.StackProfileParameter, bindings []client.ParameterBinding) []bindingPreview {
	explicit := map[string]string{}
	order := []string{}
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Value) == "" {
			continue
		}
		if _, seen := explicit[binding.Name]; !seen {
			order = append(order, binding.Name)
		}
		explicit[binding.Name] = binding.Value
	}
	byName := map[string]client.StackProfileParameter{}
	for _, parameter := range parameters {
		byName[parameter.Name] = parameter
	}

	type derivedValue struct{ value, source string }
	derived := map[string]derivedValue{}
	for _, selector := range parameters {
		if len(selector.Options) == 0 || selector.Type == "secret" {
			continue
		}
		selection := strings.TrimSpace(explicit[selector.Name])
		if selection == "" && selector.Default != nil {
			selection = strings.TrimSpace(*selector.Default)
		}
		if selection == "" {
			continue
		}
		for _, option := range selector.Options {
			if option.Value != selection {
				continue
			}
			for _, target := range sortedAssignmentTargets(option.Sets) {
				targetParameter, declared := byName[target]
				if !declared || targetParameter.Type == "secret" || len(targetParameter.Options) > 0 {
					continue
				}
				derived[target] = derivedValue{option.Sets[target], fmt.Sprintf("choice %s=%s", selector.Name, option.Value)}
			}
		}
	}

	rows := make([]bindingPreview, 0, len(parameters)+len(order))
	for _, parameter := range parameters {
		row := bindingPreview{Name: parameter.Name, Required: parameter.Required}
		switch {
		case explicit[parameter.Name] != "":
			row.Value, row.Source = explicit[parameter.Name], "--set"
		case derived[parameter.Name].source != "":
			row.Value, row.Source = derived[parameter.Name].value, derived[parameter.Name].source
		case parameter.Default != nil && strings.TrimSpace(*parameter.Default) != "":
			row.Value, row.Source = *parameter.Default, "default"
		default:
			row.Source = "unset"
		}
		if parameter.Type == "secret" && row.Value != "" {
			row.Value = "********"
		}
		rows = append(rows, row)
	}
	for _, name := range order {
		if _, declared := byName[name]; declared {
			continue
		}
		rows = append(rows, bindingPreview{Name: name, Value: explicit[name], Source: "--set (not an input of this version)"})
	}
	return rows
}

func unsetRequiredInputs(rows []bindingPreview) []string {
	missing := []string{}
	for _, row := range rows {
		if row.Required && row.Source == "unset" {
			missing = append(missing, row.Name)
		}
	}
	return missing
}

func init() {
	stackProfileDraftsOptionsSetCmd.Flags().String("parameter", "", "Input the choice belongs to")
	stackProfileDraftsOptionsSetCmd.Flags().String("value", "", "Value the input takes when this choice is picked")
	stackProfileDraftsOptionsSetCmd.Flags().String("title", "", "Label shown for the choice (optional)")
	stackProfileDraftsOptionsSetCmd.Flags().String("description", "", "Why someone would pick this choice (optional)")
	stackProfileDraftsOptionsSetCmd.Flags().StringArray("set", nil, "Input this choice answers: name=value (repeatable)")
	stackProfileDraftsOptionsSetCmd.Flags().StringArray("unset", nil, "Stop this choice answering an input: name (repeatable)")
	_ = stackProfileDraftsOptionsSetCmd.MarkFlagRequired("parameter")
	_ = stackProfileDraftsOptionsSetCmd.MarkFlagRequired("value")

	stackProfileDraftsOptionsRemoveCmd.Flags().String("parameter", "", "Input the choice belongs to")
	stackProfileDraftsOptionsRemoveCmd.Flags().String("value", "", "Value of the choice to remove")
	_ = stackProfileDraftsOptionsRemoveCmd.MarkFlagRequired("parameter")
	_ = stackProfileDraftsOptionsRemoveCmd.MarkFlagRequired("value")

	stackProfileDraftsOptionsCmd.AddCommand(stackProfileDraftsOptionsSetCmd, stackProfileDraftsOptionsRemoveCmd)
	stackProfileDraftsCmd.AddCommand(stackProfileDraftsOptionsCmd)
	registerStructuredOutputFlags(stackProfileDraftsOptionsSetCmd, stackProfileDraftsOptionsRemoveCmd)
}

// validateParameterType is shared by annotate's --type.
func validateParameterType(parameterType string) error {
	if slices.Contains(parameterTypeChoices, parameterType) {
		return nil
	}
	return withExitCode(exitUsage, fmt.Errorf("--type must be one of %s", strings.Join(parameterTypeChoices, ", ")))
}
