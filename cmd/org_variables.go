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
  ankra org variables delete DB_HOST

Variable resolution order at deploy time is stack > cluster > organisation; a
more specific scope overrides a less specific one.`,
}

var orgVariablesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all organisation variables",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		outRaw, _ := cmd.Flags().GetString("output")
		out, err := parseOutputFormat(outRaw)
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
		outRaw, _ := cmd.Flags().GetString("output")
		out, err := parseOutputFormat(outRaw)
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

The value can also be read from stdin by passing "-":

  echo "secret-token" | ankra org variables set API_TOKEN -`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		rawValue := args[1]
		descriptionFlag := cmd.Flags().Lookup("description")
		description := descriptionFlag.Value.String()
		descriptionExplicit := descriptionFlag.Changed

		value, err := readVariableValue(cmd.InOrStdin(), rawValue)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		_, err = apiClient.CreateOrganisationVariable(ctx, name, value, description)
		if err == nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Organisation variable %q created.\n", name)
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
		if _, err := apiClient.UpdateOrganisationVariable(ctx, name, value, description); err != nil {
			return fmt.Errorf("update organisation variable: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Organisation variable %q updated.\n", name)
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
		fmt.Fprintf(cmd.OutOrStdout(), "Organisation variable %q deleted.\n", name)
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
		defer enc.Close()
		return enc.Encode(client.OrganisationVariablesListResult{Variables: vars})
	}
	if len(vars) == 0 {
		fmt.Fprintln(out, "No organisation variables.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"NAME", "VALUE", "DESCRIPTION", "UPDATED"})
	for _, v := range vars {
		t.AppendRow(table.Row{v.Name, truncateForDisplay(v.Value, 40), v.Description, v.UpdatedAt.Format(time.RFC3339)})
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
		defer enc.Close()
		return enc.Encode(client.OrganisationVariableResult{Variable: v})
	}
	fmt.Fprintf(out, "Name:        %s\nValue:       %s\nDescription: %s\nUpdated:     %s\n",
		v.Name, v.Value, v.Description, v.UpdatedAt.Format(time.RFC3339))
	return nil
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
	for _, c := range []*cobra.Command{orgVariablesListCmd, orgVariablesGetCmd} {
		c.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")
	}
	orgVariablesSetCmd.Flags().String("description", "", "Optional human-readable description")
	orgVariablesDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	orgVariablesCmd.AddCommand(orgVariablesListCmd)
	orgVariablesCmd.AddCommand(orgVariablesGetCmd)
	orgVariablesCmd.AddCommand(orgVariablesSetCmd)
	orgVariablesCmd.AddCommand(orgVariablesDeleteCmd)
	orgCmd.AddCommand(orgVariablesCmd)
}
