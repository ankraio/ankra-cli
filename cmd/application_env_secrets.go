package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// The application environment-secret commands. An application's generated
// manifests declare the environment its workloads read; these set the values
// behind those keys, and apply them to what is already running. This is the
// platform answer to the `kubectl create secret generic <app>-env
// --from-literal=...` line the setup pull request used to end on.
//
// A value only ever travels inbound. There is no route that hands a stored
// value back, so `list` reports keys and their state and never a value - and
// nothing here prints one either.

func newApplicationEnvSecretsCommand() *cobra.Command {
	envSecretsCommand := &cobra.Command{
		Use:     "env-secrets",
		Aliases: []string{"env-secret"},
		Short:   "Manage the environment secrets an application's manifests read",
		Long: `Manage the environment secrets an application's manifests read.

The keys come from the application's generated manifests. Setting a value
stores it; it is sealed into a running deployment only when you apply it, so a
set followed by no apply changes nothing about the workload.

Values never travel outbound: 'list' reports which keys exist and whether each
has a value, never the value itself.`,
	}
	envSecretsCommand.AddCommand(
		newApplicationEnvSecretsListCommand(),
		newApplicationEnvSecretsSetCommand(),
		newApplicationEnvSecretsDeleteCommand(),
		newApplicationEnvSecretsApplyCommand(),
	)
	return envSecretsCommand
}

func newApplicationEnvSecretsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:   "list <application-id>",
		Short: "List the environment secret keys an application needs, and their state",
		Long: `List the environment secret keys an application needs, and their state.

Reports the keys the application's generated manifests declare and whether each
one has a stored value. The values themselves are never returned.`,
		Example: "  ankra application env-secrets list 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, listError := apiClient.ListApplicationEnvSecrets(command.Context(),
				strings.TrimSpace(arguments[0]))
			if listError != nil {
				return listError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newApplicationEnvSecretsSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application-id> <KEY>",
		Short: "Store the value of one environment secret",
		Long: `Store the value of one environment secret.

Pass the value with --value, pipe it on stdin, or omit both to be prompted for
it without echo. Prefer stdin or the prompt: a --value on the command line is
recorded in your shell history and, on a shared host, in the process table.

Storing a value does not reach a running workload. Run 'env-secrets apply' to
seal the stored values into the application's deployments and roll them.`,
		Example: `  ankra application env-secrets set <application-id> DATABASE_URL --value postgres://...

  printf '%s' "$TOKEN" | ankra application env-secrets set <application-id> API_TOKEN`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			outputFormatting, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			applicationID := strings.TrimSpace(arguments[0])
			secretKey := strings.TrimSpace(arguments[1])
			if keyError := validateEnvSecretKey(secretKey); keyError != nil {
				return keyError
			}
			value, valueError := resolveEnvSecretValue(command, secretKey)
			if valueError != nil {
				return valueError
			}
			payload, setError := apiClient.SetApplicationEnvSecret(command.Context(),
				applicationID, secretKey, value)
			if setError != nil {
				return setError
			}
			// The hint is for a person. A structured invocation is a script,
			// which gets a clean stderr rather than prose it has to ignore.
			if outputFormatting == outputDefault {
				_, _ = fmt.Fprintf(command.ErrOrStderr(),
					"Stored. Run 'ankra application env-secrets apply %s' to seal it into the running deployments.\n",
					applicationID)
			}
			return renderApplicationPayload(command, payload)
		},
	}
	setCommand.Flags().String("value", "", "Secret value (omit to read stdin or be prompted)")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

// maxEnvSecretKeyLength mirrors the platform's own bound on one Secret key
// (enginekit/manifestenv.MaxKeyLength).
const maxEnvSecretKeyLength = 63

// validateEnvSecretKey refuses a key the platform would refuse anyway, before
// the key is interpolated into a request path.
//
// The rule is the environment-variable rule the backend enforces
// (manifestenv.IsValidKey): a letter or underscore, then letters, digits and
// underscores. Checking it here is not duplicated validation for its own
// sake - url.PathEscape does not escape dot segments, so a key of ".." would
// otherwise build ".../env-secrets/.." and a server or proxy that normalises
// request paths could resolve that onto the application resource itself,
// which on the DELETE verb is the delete-application route.
func validateEnvSecretKey(secretKey string) error {
	if secretKey == "" || len(secretKey) > maxEnvSecretKeyLength {
		return withExitCode(exitUsage, fmt.Errorf(
			"%q is not a valid environment variable name: it must be 1 to %d characters",
			secretKey, maxEnvSecretKeyLength))
	}
	for index := 0; index < len(secretKey); index++ {
		symbol := secretKey[index]
		isLetter := (symbol >= 'A' && symbol <= 'Z') || (symbol >= 'a' && symbol <= 'z')
		isDigit := symbol >= '0' && symbol <= '9'
		if isLetter || symbol == '_' || (isDigit && index > 0) {
			continue
		}
		return withExitCode(exitUsage, fmt.Errorf(
			"%q is not a valid environment variable name: use letters, digits and underscores, "+
				"and do not start with a digit", secretKey))
	}
	return nil
}

// resolveEnvSecretValue reads the value from --value, from piped stdin, or
// from a masked prompt, in that order.
//
// It deliberately does not reuse resolveSecretInput, which trims the value on
// every path. That is right for an API key and wrong here: an application's
// environment holds arbitrary values - PEM blocks, passwords that end in a
// space, base64 with padding - and silently trimming one stores a value the
// workload will never match, with nothing to see afterwards because no route
// hands a stored value back. So --value is taken verbatim, and a piped value
// loses exactly the one line ending the pipe added rather than all of its
// surrounding whitespace.
//
// The interactive prompt is the exception: it cannot show what it captured, a
// typed trailing space is a typo rather than a secret, and Ctrl+C there is a
// cancellation rather than an empty value.
func resolveEnvSecretValue(command *cobra.Command, secretKey string) (string, error) {
	if command.Flags().Changed("value") {
		value, _ := command.Flags().GetString("value")
		// An explicitly empty --value is refused, so that the flag path
		// agrees with the stdin and prompt paths rather than being the one
		// way to store an empty secret by accident. The script footgun is
		// --value "$UNSET_VAR": it stores a value the workload never
		// matches, and no route hands a stored value back to show it.
		if value == "" {
			return "", withExitCode(exitUsage, fmt.Errorf(
				"--value is empty: pass a value, or use 'env-secrets delete %s' to clear the stored one",
				secretKey))
		}
		return value, nil
	}
	input := command.InOrStdin()
	if file, isFile := input.(*os.File); isFile && readline.IsTerminal(int(file.Fd())) {
		prompt := promptui.Prompt{Label: "value of " + secretKey, Mask: '*', Stdin: file}
		typed, promptError := prompt.Run()
		if promptError != nil {
			if isPromptCancellation(promptError) {
				return "", errCancelled
			}
			return "", fmt.Errorf("reading the value of %s: %w", secretKey, promptError)
		}
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return "", withExitCode(exitUsage,
				fmt.Errorf("the value of %s is required", secretKey))
		}
		return typed, nil
	}
	piped, readError := io.ReadAll(bufio.NewReader(input))
	if readError != nil && !errors.Is(readError, io.EOF) {
		return "", fmt.Errorf("reading the value of %s from stdin: %w", secretKey, readError)
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(piped), "\n"), "\r")
	if value == "" {
		return "", withExitCode(exitUsage, fmt.Errorf(
			"the value of %s is required: pass --value, pipe it on stdin, or run interactively to be prompted",
			secretKey))
	}
	return value, nil
}

func newApplicationEnvSecretsDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:   "delete <application-id> <KEY>",
		Short: "Clear the stored value of one environment secret",
		Long: `Clear the stored value of one environment secret.

The key stays declared by the application's manifests; only the value Ankra
holds for it is removed. Deployments already running keep the value that was
sealed into them until the next apply.`,
		Example: "  ankra application env-secrets delete <application-id> API_TOKEN",
		Args:    cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID := strings.TrimSpace(arguments[0])
			secretKey := strings.TrimSpace(arguments[1])
			if keyError := validateEnvSecretKey(secretKey); keyError != nil {
				return keyError
			}
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(
				command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("Clear the stored value of %q on application %q? "+
					"Ankra cannot show it to you again, so it cannot be recovered from here. [y/N]: ",
					secretKey, applicationID),
				yes,
			); confirmError != nil {
				return confirmError
			}
			payload, deleteError := apiClient.DeleteApplicationEnvSecret(command.Context(),
				applicationID, secretKey)
			if deleteError != nil {
				return deleteError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	deleteCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(deleteCommand)
	return deleteCommand
}

func newApplicationEnvSecretsApplyCommand() *cobra.Command {
	applyCommand := &cobra.Command{
		Use:   "apply <application-id>",
		Short: "Seal the stored environment secrets into the running deployments",
		Long: `Seal the stored environment secrets into the running deployments.

Re-seals the values Ankra already holds into every deployment of the
application and rolls the workloads that read them. It sends nothing: the
values applied are the ones already stored.

The endpoint answers 409 when the request does not apply to this application's
state - nothing set, not deployed yet, tearing down, or a deployment that
cannot be sealed - and the reason is reported as the error message.`,
		Example: "  ankra application env-secrets apply 23298741-6a5a-401a-a681-66f31fbdebe1",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, applyError := apiClient.ApplyApplicationEnvSecrets(command.Context(),
				strings.TrimSpace(arguments[0]))
			if applyError != nil {
				return applyError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(applyCommand)
	return applyCommand
}
