package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var orgVariablesCmd = &cobra.Command{
	Use:   "variables",
	Short: "Manage organisation-scoped variables",
	Long: `Manage organisation-scoped variables that are available to every cluster
in the organisation as template substitutions in stack manifests and addon
values.

  ankra org variables list
  ankra org variables get DB_HOST
  ankra org variables set DB_HOST db.prod.example.com --description "Primary database"
  ankra org variables set API_TOKEN - --secret < token.txt
  ankra org variables reveal API_TOKEN
  ankra org variables delete DB_HOST

A secret variable's value is never shown by list or get; reveal reads it back
for members who can change variables, and every reveal is audited.

Variable resolution order at deploy time is stack > cluster > organisation; a
more specific scope overrides a less specific one.`,
}

var orgVariablesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all organisation variables",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListOrganisationVariables(ctx)
		if err != nil {
			return fmt.Errorf("list organisation variables: %w", err)
		}
		return renderOrgVariables(cmd.OutOrStdout(), list.Variables, out)
	},
}

var orgVariablesGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get a single organisation variable",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListOrganisationVariables(ctx)
		if err != nil {
			return fmt.Errorf("list organisation variables: %w", err)
		}
		for _, v := range list.Variables {
			if v.Name == name {
				return renderSingleOrgVariable(cmd.OutOrStdout(), v, out)
			}
		}
		return fmt.Errorf("organisation variable %q not found", name)
	},
}

var orgVariablesSetCmd = &cobra.Command{
	Use:   "set <name> <value>",
	Short: "Create or update an organisation variable",
	Long: `Create or update an organisation variable (upsert). If the variable
does not exist it is created; otherwise its value (and description, when
supplied) is updated.

--secret creates the variable as a secret, or marks an existing one secret:
list and get then never show its value, and only "ankra org variables reveal"
reads it back. --secret=false makes an existing secret variable plain again.
Without the flag an update keeps the variable's current setting.

The value can also be read from stdin by passing "-":

  echo "secret-token" | ankra org variables set API_TOKEN - --secret`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		rawValue := args[1]
		descriptionFlag := cmd.Flags().Lookup("description")
		description := descriptionFlag.Value.String()
		descriptionExplicit := descriptionFlag.Changed
		isSecret, _ := cmd.Flags().GetBool("secret")
		var secretUpdate *bool
		if cmd.Flags().Changed("secret") {
			secretUpdate = &isSecret
		}

		value, err := readVariableValue(cmd.InOrStdin(), rawValue)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		_, err = apiClient.CreateOrganisationVariable(ctx, name, value, description, isSecret)
		if err == nil {
			if isSecret {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Secret organisation variable %q created.\n", name)
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Organisation variable %q created.\n", name)
			return nil
		}
		if !errors.Is(err, client.ErrVariableDuplicate) {
			return fmt.Errorf("create organisation variable: %w", err)
		}

		// Upsert fallback: update an existing variable. Preserve the existing
		// description when --description was not explicitly provided so a
		// `set` without --description doesn't accidentally clear it.
		if !descriptionExplicit {
			list, listErr := apiClient.ListOrganisationVariables(ctx)
			if listErr == nil {
				for _, v := range list.Variables {
					if v.Name == name {
						description = v.Description
						break
					}
				}
			}
		}
		if _, err := apiClient.UpdateOrganisationVariable(ctx, name, value, description, secretUpdate); err != nil {
			return fmt.Errorf("update organisation variable: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Organisation variable %q updated.\n", name)
		return nil
	},
}

var orgVariablesDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an organisation variable",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		yes, _ := cmd.Flags().GetBool("yes")

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete organisation variable %q? [y/N]: ", name),
			yes,
		); err != nil {
			return err
		}

		if err := apiClient.DeleteOrganisationVariable(ctx, name); err != nil {
			if errors.Is(err, client.ErrVariableNotFound) {
				return fmt.Errorf("organisation variable %q not found", name)
			}
			return fmt.Errorf("delete organisation variable: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Organisation variable %q deleted.\n", name)
		return nil
	},
}

var orgVariablesRevealCmd = &cobra.Command{
	Use:   "reveal <name>",
	Short: "Print an organisation variable's stored value, secret or not",
	Long: `Print one organisation variable's stored value. This is the only way to
read back a secret variable: list and get never show it.

Revealing needs permission to change variables, and every reveal is recorded
in the organisation's audit log. The value is printed on its own line, so it
can be captured in a script:

  TOKEN="$(ankra org variables reveal API_TOKEN)"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		revealed, revealError := apiClient.RevealOrganisationVariable(ctx, name)
		if revealError != nil {
			if errors.Is(revealError, client.ErrVariableNotFound) {
				return withExitCode(exitNotFound, fmt.Errorf("organisation variable %q not found", name))
			}
			return fmt.Errorf("reveal organisation variable: %w", revealError)
		}
		switch format {
		case outputJSON:
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(revealed)
		case outputYAML:
			encoder := yaml.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent(2)
			defer func() { _ = encoder.Close() }()
			return encoder.Encode(revealed)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), revealed.Value)
		return nil
	},
}

// readVariableValue resolves the variable value, reading from stdin if the
// caller passed "-". Used by both org and cluster set commands.
func readVariableValue(in io.Reader, raw string) (string, error) {
	if raw != "-" {
		return raw, nil
	}
	data, err := io.ReadAll(io.LimitReader(in, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("read value from stdin: %w", err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func renderOrgVariables(out io.Writer, vars []client.OrganisationVariable, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(client.OrganisationVariablesListResult{Variables: vars})
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(client.OrganisationVariablesListResult{Variables: vars})
	}
	if len(vars) == 0 {
		_, _ = fmt.Fprintln(out, "No organisation variables.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"NAME", "VALUE", "SECRET", "DESCRIPTION", "UPDATED"})
	for _, v := range vars {
		t.AppendRow(table.Row{v.Name, organisationVariableDisplayValue(v), yesNo(v.IsSecret), v.Description, v.UpdatedAt.Format(time.RFC3339)})
	}
	t.Render()
	return nil
}

func renderSingleOrgVariable(out io.Writer, v client.OrganisationVariable, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(client.OrganisationVariableResult{Variable: v})
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(client.OrganisationVariableResult{Variable: v})
	}
	value := v.Value
	if v.IsSecret {
		value = fmt.Sprintf("%s (run \"ankra org variables reveal %s\")", hiddenSecretValue, v.Name)
	}
	_, _ = fmt.Fprintf(out, "Name:        %s\nValue:       %s\nSecret:      %s\nDescription: %s\nUpdated:     %s\n",
		v.Name, value, yesNo(v.IsSecret), v.Description, v.UpdatedAt.Format(time.RFC3339))
	return nil
}

// hiddenSecretValue stands in for a secret variable's value, which the
// backend returns only from the reveal endpoint.
const hiddenSecretValue = "(hidden)"

// organisationVariableDisplayValue is the table cell for a variable's value:
// the truncated value, or the hidden marker for a secret variable.
func organisationVariableDisplayValue(variable client.OrganisationVariable) string {
	if variable.IsSecret {
		return hiddenSecretValue
	}
	return truncateForDisplay(variable.Value, 40)
}

// truncateForDisplay limits long values when rendering tables so secrets that
// happen to fit on one line don't blow up terminal output. The full value is
// still available via `get -o json` or `get`.
func truncateForDisplay(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func init() {
	registerStructuredOutputFlags(orgVariablesListCmd, orgVariablesGetCmd, orgVariablesRevealCmd)
	orgVariablesSetCmd.Flags().String("description", "", "Optional human-readable description")
	orgVariablesSetCmd.Flags().Bool("secret", false, "Store the value as a secret that list and get never show (--secret=false makes an existing secret plain)")
	orgVariablesDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	orgVariablesCmd.AddCommand(orgVariablesListCmd)
	orgVariablesCmd.AddCommand(orgVariablesGetCmd)
	orgVariablesCmd.AddCommand(orgVariablesSetCmd)
	orgVariablesCmd.AddCommand(orgVariablesRevealCmd)
	orgVariablesCmd.AddCommand(orgVariablesDeleteCmd)
	orgCmd.AddCommand(orgVariablesCmd)
}
