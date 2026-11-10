package cmd

// `ankra services credentials ...` (ankra-hq41xb.15): the CLI surface over
// /api/v1/org/service-admission/credentials (families and their values) and
// /credential-grants, the registry of credentials a managed service is given
// as an input.
//
// A family is a named credential. Its values are immutable versions; one is
// active. A grant is a purpose-bound reference to a value for one input of
// one package version, revocable on its own. No route returns a value or a
// store path. A new value is never a flag: it comes from stdin
// (--value-stdin) or a masked prompt, and it is never printed, including in
// an error.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func newServicesCredentialsCommand() *cobra.Command {
	credentialsCommand := &cobra.Command{
		Use:     "credentials",
		Aliases: []string{"credential"},
		Short:   "Manage the credentials managed services are given as inputs",
		Long: `The registry of credentials a managed service can be given as an input, for
example the password of a database you already run.

A family is one named credential. Its values are immutable versions, and one
of them is active: storing a new value rotates the family, which retires the
previous value, revokes its grants and issues replacements. A grant is a
reference to a value for one input of one package version, and each grant is
revoked on its own.

No command here shows a value: the platform stores it and returns metadata
only. This registry does not connect or restart a workload.`,
		Example: `  ankra services credentials create orders-db-password
  cat password.txt | ankra services credentials values add orders-db-password --first --value-stdin
  ankra services credentials grants issue orders-db-password --package-digest sha256:... --input-name DATABASE_PASSWORD --mode existing`,
	}
	valuesCommand := &cobra.Command{
		Use:     "values",
		Aliases: []string{"value"},
		Short:   "List a credential family's versions or store a new one",
	}
	valuesCommand.AddCommand(newServicesCredentialValuesListCommand(), newServicesCredentialValuesAddCommand())
	grantsCommand := &cobra.Command{
		Use:     "grants",
		Aliases: []string{"grant"},
		Short:   "List, issue and revoke references to a credential value",
	}
	grantsCommand.AddCommand(newServicesCredentialGrantsListCommand(), newServicesCredentialGrantsIssueCommand(),
		newServicesCredentialGrantsRevokeCommand())
	credentialsCommand.AddCommand(newServicesCredentialsListCommand(), newServicesCredentialsGetCommand(),
		newServicesCredentialsCreateCommand(), valuesCommand, grantsCommand)
	return credentialsCommand
}

func listAllServiceCredentialFamilies(command *cobra.Command) ([]client.ServiceCredentialFamily, error) {
	return collectServicePages(func(after string) ([]client.ServiceCredentialFamily, *string, error) {
		page, pageError := apiClient.ListServiceCredentialFamilies(command.Context(),
			client.ServicePageOptions{Limit: servicePageLimit, After: after})
		if pageError != nil {
			return nil, nil, pageError
		}
		return page.Items, page.NextCursor, nil
	})
}

// resolveServiceCredentialFamily reads the family a reference names: its id,
// or its name as 'ankra services credentials list' shows it.
func resolveServiceCredentialFamily(command *cobra.Command, reference string) (*client.ServiceCredentialFamily, error) {
	reference = strings.TrimSpace(reference)
	if isLikelyClusterID(reference) {
		family, getError := apiClient.GetServiceCredentialFamily(command.Context(), reference)
		if getError != nil {
			return nil, fmt.Errorf("reading credential family %s: %w", reference, getError)
		}
		return family, nil
	}
	families, listError := listAllServiceCredentialFamilies(command)
	if listError != nil {
		return nil, fmt.Errorf("listing credential families: %w", listError)
	}
	for _, family := range families {
		if family.Name == reference {
			return &family, nil
		}
	}
	return nil, withExitCode(exitNotFound, fmt.Errorf(
		"no credential family is named %q; see 'ankra services credentials list'", reference))
}

func printServiceCredentialFamily(out io.Writer, family client.ServiceCredentialFamily) {
	_, _ = fmt.Fprintf(out, "Credential family: %s (%s)\n", family.Name, family.ID)
	_, _ = fmt.Fprintf(out, "  Created:      %s\n", family.CreatedAt)
	_, _ = fmt.Fprintf(out, "  Active value: %s\n", valueOr(family.ActiveValueID, "none yet"))
}

func newServicesCredentialsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List credential families",
		Long:    `List the organisation's credential families: each family's id, name and the id of its active value. Values are never shown.`,
		Example: "  ankra services credentials list",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			families, listError := listAllServiceCredentialFamilies(command)
			if listError != nil {
				return fmt.Errorf("listing credential families: %w", listError)
			}
			if families == nil {
				families = []client.ServiceCredentialFamily{}
			}
			if rendered, renderError := renderStructured(command, families); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(families) == 0 {
				_, _ = fmt.Fprintln(out, "No credential families. Create one with 'ankra services credentials create <name>'.")
				return nil
			}
			rows := make([]table.Row, 0, len(families))
			for _, family := range families {
				rows = append(rows, table.Row{family.ID, family.Name, valueOr(family.ActiveValueID, "none yet"), family.CreatedAt})
			}
			renderRoundedTable(out, table.Row{"ID", "Name", "Active value", "Created"}, rows)
			return nil
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newServicesCredentialsGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:     "get <family>",
		Short:   "Show one credential family",
		Long:    `Show one credential family by its id or name: when it was created and the id of its active value. The value is never shown.`,
		Example: "  ankra services credentials get orders-db-password",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			family, resolveError := resolveServiceCredentialFamily(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			if rendered, renderError := renderStructured(command, family); rendered || renderError != nil {
				return renderError
			}
			printServiceCredentialFamily(command.OutOrStdout(), *family)
			return nil
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newServicesCredentialsCreateCommand() *cobra.Command {
	createCommand := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an empty credential family",
		Long: `Create a credential family. <name> is lowercase letters, digits and dashes,
starting with a letter, at most 63 characters. The family starts without a
value; store one with 'ankra services credentials values add <name> --first'.`,
		Example: "  ankra services credentials create orders-db-password",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			name := strings.TrimSpace(arguments[0])
			family, createError := apiClient.CreateServiceCredentialFamily(command.Context(), name)
			if createError != nil {
				return fmt.Errorf("creating credential family %q: %w", name, createError)
			}
			if rendered, renderError := renderStructured(command, family); rendered || renderError != nil {
				return renderError
			}
			printServiceCredentialFamily(command.OutOrStdout(), *family)
			return nil
		},
	}
	registerStructuredOutputFlags(createCommand)
	return createCommand
}

func newServicesCredentialValuesListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list <family>",
		Aliases: []string{"ls"},
		Short:   "List a credential family's versions",
		Long: `List the metadata of a family's values: each version's id, state (pending,
active, retired, failed or deleted), when it was activated and retired, and
whether the platform still retains it. The values themselves are never shown.`,
		Example: "  ankra services credentials values list orders-db-password",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			family, resolveError := resolveServiceCredentialFamily(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			values, listError := collectServicePages(func(after string) ([]client.ServiceCredentialValue, *string, error) {
				page, pageError := apiClient.ListServiceCredentialValues(command.Context(), family.ID,
					client.ServicePageOptions{Limit: servicePageLimit, After: after})
				if pageError != nil {
					return nil, nil, pageError
				}
				return page.Items, page.NextCursor, nil
			})
			if listError != nil {
				return fmt.Errorf("listing the values of credential family %q: %w", family.Name, listError)
			}
			if values == nil {
				values = []client.ServiceCredentialValue{}
			}
			if rendered, renderError := renderStructured(command, values); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(values) == 0 {
				_, _ = fmt.Fprintf(out, "Credential family '%s' has no value yet. Store one with 'ankra services credentials values add %s --first'.\n",
					family.Name, family.Name)
				return nil
			}
			rows := make([]table.Row, 0, len(values))
			for _, value := range values {
				rows = append(rows, table.Row{value.ID, value.State, value.CreatedAt, valueOr(value.ActivatedAt, "-"),
					valueOr(value.RetiredAt, "-"), value.Retained})
			}
			renderRoundedTable(out, table.Row{"ID", "State", "Created", "Activated", "Retired", "Retained"}, rows)
			return nil
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func printServiceCredentialGrants(out io.Writer, heading string, grants []client.ServiceCredentialGrant) {
	if len(grants) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\n%s:\n", heading)
	for _, grant := range grants {
		_, _ = fmt.Fprintf(out, "  %s  %s of %s (%s)\n", grant.ID, grant.InputName, grant.PackageDigest, grant.Mode)
	}
}

func newServicesCredentialValuesAddCommand() *cobra.Command {
	addCommand := &cobra.Command{
		Use:   "add <family> --first | --expected-value-id <id>",
		Short: "Store a new value in a credential family (create or rotate)",
		Long: `Store a new value in a credential family. The value becomes the family's active
value; the previous one is retired, its grants are revoked and replacement
grants are issued, which the command lists.

Say what you expect the family to hold, so a repeated or stale command cannot
replace a newer value:

  --first                   the family has no active value yet
  --expected-value-id <id>  the active value you are replacing, as
                            'ankra services credentials get <family>' shows it

The platform refuses (409) when the family's state is not what you named.

The value is never taken as an argument. Pipe it on stdin with --value-stdin,
or run in a terminal to be asked for it with the input hidden. It is stored by
the platform and never shown again.`,
		Example: `  cat password.txt | ankra services credentials values add orders-db-password --first --value-stdin
  cat new-password.txt | ankra services credentials values add orders-db-password \
    --expected-value-id 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --value-stdin`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			first, _ := command.Flags().GetBool("first")
			expectedValueID, _ := command.Flags().GetString("expected-value-id")
			expectedValueID = strings.TrimSpace(expectedValueID)
			if first == (expectedValueID != "") {
				return withExitCode(exitUsage, errors.New(
					"pass exactly one of --first (the family has no active value yet) and --expected-value-id <id> (the active value being replaced)"))
			}
			family, resolveError := resolveServiceCredentialFamily(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			value, valueError := readSecretFromStdinOrPrompt(command, "value-stdin", "credential value")
			if valueError != nil {
				return valueError
			}
			var expected *string
			if !first {
				expected = &expectedValueID
			}
			rotation, addError := apiClient.AddServiceCredentialValue(command.Context(), family.ID, value, expected)
			if addError != nil {
				return redactSecret(fmt.Errorf("storing a value in credential family %q: %w", family.Name, addError), value)
			}
			if rendered, renderError := renderStructured(command, rotation); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Value %s stored in credential family '%s' (%s).\n", rotation.Value.ID, family.Name, rotation.Value.State)
			printServiceCredentialGrants(out, "Grants revoked with the previous value", rotation.Replaced)
			printServiceCredentialGrants(out, "Replacement grants issued", rotation.Granted)
			return nil
		},
	}
	addCommand.Flags().Bool("first", false, "The family has no active value yet")
	addCommand.Flags().String("expected-value-id", "", "The id of the active value being replaced")
	addCommand.Flags().Bool("value-stdin", false, "Read the credential value from stdin")
	registerStructuredOutputFlags(addCommand)
	return addCommand
}

func newServicesCredentialGrantsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list <family>",
		Aliases: []string{"ls"},
		Short:   "List the grants of a credential family",
		Long: `List a family's grants: which value each references, for which input of which
package version, in which mode, and whether it has been revoked.`,
		Example: "  ankra services credentials grants list orders-db-password",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			family, resolveError := resolveServiceCredentialFamily(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			grants, listError := collectServicePages(func(after string) ([]client.ServiceCredentialGrant, *string, error) {
				page, pageError := apiClient.ListServiceCredentialGrants(command.Context(), family.ID,
					client.ServicePageOptions{Limit: servicePageLimit, After: after})
				if pageError != nil {
					return nil, nil, pageError
				}
				return page.Items, page.NextCursor, nil
			})
			if listError != nil {
				return fmt.Errorf("listing the grants of credential family %q: %w", family.Name, listError)
			}
			if grants == nil {
				grants = []client.ServiceCredentialGrant{}
			}
			if rendered, renderError := renderStructured(command, grants); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(grants) == 0 {
				_, _ = fmt.Fprintf(out, "Credential family '%s' has no grants.\n", family.Name)
				return nil
			}
			rows := make([]table.Row, 0, len(grants))
			for _, grant := range grants {
				rows = append(rows, table.Row{grant.ID, grant.InputName, grant.Mode, grant.PackageDigest, grant.ValueID,
					valueOr(grant.RevokedAt, "-")})
			}
			renderRoundedTable(out, table.Row{"ID", "Input", "Mode", "Package digest", "Value", "Revoked"}, rows)
			return nil
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newServicesCredentialGrantsIssueCommand() *cobra.Command {
	issueCommand := &cobra.Command{
		Use:   "issue <family> --package-digest <sha256:...> --input-name <NAME> --mode hosted|customer|existing",
		Short: "Issue a reference to a credential value for one package input",
		Long: `Issue a grant: a reference to one of the family's values, bound to one input of
one package version. --package-digest is the package version's digest
(sha256:...), --input-name the input the credential is for (upper case, as the
package declares it) and --mode how the service is run: hosted, customer or
existing.

The grant references the family's active value unless --value-id names a
retained one. Each grant is revoked on its own, and a family holds at most 128
active grants.`,
		Example: `  ankra services credentials grants issue orders-db-password \
    --package-digest sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08 \
    --input-name DATABASE_PASSWORD --mode existing`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			packageDigest, digestError := requiredTextFlag(command, "package-digest", "the package version's digest (sha256:...)")
			if digestError != nil {
				return digestError
			}
			inputName, inputError := requiredTextFlag(command, "input-name", "the package input the credential is for")
			if inputError != nil {
				return inputError
			}
			mode, _ := command.Flags().GetString("mode")
			if mode != "hosted" && mode != "customer" && mode != "existing" {
				return withExitCode(exitUsage, fmt.Errorf("--mode must be hosted, customer or existing, got %q", mode))
			}
			valueID, _ := command.Flags().GetString("value-id")
			family, resolveError := resolveServiceCredentialFamily(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			grant, issueError := apiClient.IssueServiceCredentialGrant(command.Context(), client.IssueServiceCredentialGrantRequest{
				FamilyID: family.ID, ValueID: strings.TrimSpace(valueID), PackageDigest: packageDigest, InputName: inputName, Mode: mode,
			})
			if issueError != nil {
				return fmt.Errorf("issuing a grant on credential family %q: %w", family.Name, issueError)
			}
			if rendered, renderError := renderStructured(command, grant); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Grant %s issued: %s of %s (%s) references value %s of '%s'.\n",
				grant.ID, grant.InputName, grant.PackageDigest, grant.Mode, grant.ValueID, family.Name)
			return nil
		},
	}
	issueCommand.Flags().String("package-digest", "", "The package version's digest, sha256:... (required)")
	issueCommand.Flags().String("input-name", "", "The package input the credential is for (required)")
	issueCommand.Flags().String("mode", "", "hosted, customer or existing (required)")
	issueCommand.Flags().String("value-id", "", "A retained value to reference instead of the active one")
	registerStructuredOutputFlags(issueCommand)
	return issueCommand
}

func newServicesCredentialGrantsRevokeCommand() *cobra.Command {
	revokeCommand := &cobra.Command{
		Use:   "revoke <grant-id>",
		Short: "Revoke one credential grant",
		Long: `Revoke one grant by its id. Whatever was given the reference can no longer
resolve the credential through it; the family's value and its other grants are
untouched. It asks first; --yes skips the question and is required when there
is no terminal to ask on.`,
		Example: "  ankra services credentials grants revoke 0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			grantID := strings.TrimSpace(arguments[0])
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Revoking grant %s stops whatever holds it from resolving the credential through it.", grantID)); confirmError != nil {
				return confirmError
			}
			if revokeError := apiClient.RevokeServiceCredentialGrant(command.Context(), grantID); revokeError != nil {
				return fmt.Errorf("revoking grant %s: %w", grantID, revokeError)
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Grant %s revoked.\n", grantID)
			return nil
		},
	}
	revokeCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	return revokeCommand
}
