package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// connectionProbePollInterval is how often probe --wait reads the probe again.
var connectionProbePollInterval = 3 * time.Second

func newServicesPackagesProposeCommand() *cobra.Command {
	var profileID, workspaceID, packagePath string
	command := &cobra.Command{
		Use:   "propose",
		Short: "Propose a package's connection from a running demo of its stack profile",
		Long: `Read a running demo of a stack profile on the organisation's staging cluster
and propose the connection blocks a service package made from that profile
could declare: one per Service port speaking a protocol Ankra connects
applications to, each field mapped to a key of the Secret that holds the most
of the credentials. Only Service ports and Secret key names are read, never
Secret values, and nothing is tried: probe a proposal with
'ankra services packages probe' before publishing.

With -f, the best complete proposal is written into that package file (made
by 'ankra services packages init'), replacing its connection.

A demo whose namespace could not be observed exits 1 with the reason, so an
unobserved namespace is never read as one with nothing to propose.`,
		Example: `  ankra services packages propose --profile <profile-id> --demo <workspace-id>
  ankra services packages propose --profile <profile-id> --demo <workspace-id> -f dragonfly-cache.json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			proposal, readError := apiClient.GetStackProfileDemoConnectionProposal(command.Context(), profileID, workspaceID)
			if readError != nil {
				return fmt.Errorf("reading the demo's connection proposal: %w", readError)
			}
			if rendered, renderError := renderStructured(command, proposal); renderError != nil {
				return renderError
			} else if !rendered {
				printConnectionProposal(command.OutOrStdout(), *proposal)
			}
			if !proposal.Observable {
				return fmt.Errorf("the demo's namespace could not be observed, so nothing was proposed: %s", valueOr(&proposal.Reason, "no reason given"))
			}
			if packagePath == "" {
				return nil
			}
			if packagePath == "-" {
				return withExitCode(exitUsage, errors.New("-f names the package file to rewrite; standard input cannot be written back"))
			}
			best, found := bestCompleteProposal(proposal.Proposals)
			if !found {
				return errors.New("no proposal is complete, so the package file was left as it is; fill in the unresolved fields by hand")
			}
			if writeError := writeProposedConnection(packagePath, best.Connection); writeError != nil {
				return writeError
			}
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Wrote the %s connection through %s into %s. Probe it with:\n  ankra services packages probe --profile %s --demo %s -f %s --wait\n",
				best.Connection.Protocol, primaryEndpointLabel(best.Connection), packagePath, profileID, workspaceID, packagePath)
			return nil
		},
	}
	command.Flags().StringVar(&profileID, "profile", "", "id of the stack profile the demo runs")
	command.Flags().StringVar(&workspaceID, "demo", "", "id of the running demo (its workspace id)")
	command.Flags().StringVarP(&packagePath, "file", "f", "", "write the best complete proposal into this package file")
	for _, required := range []string{"profile", "demo"} {
		_ = command.MarkFlagRequired(required)
	}
	registerStructuredOutputFlags(command)
	return command
}

func newServicesPackagesProbeCommand() *cobra.Command {
	var profileID, workspaceID, packagePath string
	var wait bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "probe",
		Short: "Try a package's connection against a running demo of its stack profile",
		Long: `Probe the connection a package file declares against a running demo of its
stack profile, before publishing: Ankra runs the protocol's readiness canary
in the demo's namespace, the same one that verifies a published package.

Without --wait the probe is only queued. With --wait the command follows it
and exits 0 only when the verdict is passed; failed and unknown (the canary
could not run, or the demo stopped) exit 1 with the reason, and a probe still
running when --timeout ends exits 5.`,
		Example: `  ankra services packages probe --profile <profile-id> --demo <workspace-id> -f dragonfly-cache.json --wait`,
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			if wait && timeout <= 0 {
				return withExitCode(exitUsage, errors.New("--timeout must be positive"))
			}
			connection, readError := packageConnection(command.InOrStdin(), packagePath)
			if readError != nil {
				return readError
			}
			ctx := command.Context()
			probe, probeError := apiClient.ProbeStackProfileDemoConnection(ctx, profileID, workspaceID, connection)
			if probeError != nil {
				return fmt.Errorf("queuing the probe: %w", probeError)
			}
			var waitError error
			if wait {
				probe, waitError = waitForConnectionProbe(command, profileID, workspaceID, probe, timeout)
			}
			if rendered, renderError := renderStructured(command, probe); renderError != nil {
				return renderError
			} else if !rendered {
				printConnectionProbe(command.OutOrStdout(), *probe)
			}
			if waitError != nil {
				return waitError
			}
			if !wait {
				_, _ = fmt.Fprintln(command.ErrOrStderr(), "The probe is queued; run again with --wait to follow it to a verdict.")
				return nil
			}
			return connectionProbeVerdictError(*probe)
		},
	}
	command.Flags().StringVar(&profileID, "profile", "", "id of the stack profile the demo runs")
	command.Flags().StringVar(&workspaceID, "demo", "", "id of the running demo (its workspace id)")
	command.Flags().StringVarP(&packagePath, "file", "f", "", "package file whose connection is probed (- for standard input)")
	command.Flags().BoolVar(&wait, "wait", false, "follow the probe to its verdict; exit 0 only if it passed")
	command.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long --wait follows the probe")
	for _, required := range []string{"profile", "demo", "file"} {
		_ = command.MarkFlagRequired(required)
	}
	registerStructuredOutputFlags(command)
	return command
}

// bestCompleteProposal is the first complete proposal, in the platform's
// order (complete first, then by confidence).
func bestCompleteProposal(proposals []client.ServiceConnectionProposal) (client.ServiceConnectionProposal, bool) {
	for _, proposal := range proposals {
		if proposal.Complete {
			return proposal, true
		}
	}
	return client.ServiceConnectionProposal{}, false
}

// writeProposedConnection replaces the connection of the package file at
// packagePath. The file is read into the contract init writes, refusing
// fields that contract does not know so none is dropped, and replaced
// atomically.
func writeProposedConnection(packagePath string, connection client.ServicePackageConnection) error {
	raw, readError := readServicePackageDocument(nil, packagePath)
	if readError != nil {
		return readError
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var contract client.ServicePackageContract
	if decodeError := decoder.Decode(&contract); decodeError != nil {
		return fmt.Errorf("the package file is not one this CLI can rewrite without losing a field (%w); copy the proposal's connection in by hand", decodeError)
	}
	contract.Connection = &connection
	encoded, encodeError := json.MarshalIndent(contract, "", "  ")
	if encodeError != nil {
		return encodeError
	}
	temporary, createError := os.CreateTemp(filepath.Dir(packagePath), ".package-*.json")
	if createError != nil {
		return fmt.Errorf("writing the package file: %w", createError)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, writeError := temporary.Write(append(encoded, '\n')); writeError != nil {
		_ = temporary.Close()
		return fmt.Errorf("writing the package file: %w", writeError)
	}
	if closeError := temporary.Close(); closeError != nil {
		return fmt.Errorf("writing the package file: %w", closeError)
	}
	return os.Rename(temporary.Name(), packagePath)
}

// packageConnection reads the connection a package document declares.
func packageConnection(stdin io.Reader, packagePath string) (client.ServicePackageConnection, error) {
	raw, readError := readServicePackageDocument(stdin, packagePath)
	if readError != nil {
		return client.ServicePackageConnection{}, readError
	}
	var document struct {
		Connection *client.ServicePackageConnection `json:"connection"`
	}
	if decodeError := json.Unmarshal(raw, &document); decodeError != nil {
		return client.ServicePackageConnection{}, fmt.Errorf("reading the package's connection: %w", decodeError)
	}
	if document.Connection == nil {
		return client.ServicePackageConnection{}, withExitCode(exitUsage, errors.New("the package declares no connection to probe"))
	}
	return *document.Connection, nil
}

// waitForConnectionProbe reads the probe until it is probed or expired, or
// until timeout ends, which exits 5 with the probe as last read.
func waitForConnectionProbe(command *cobra.Command, profileID string, workspaceID string,
	probe *client.StackProfileDemoConnectionProbe, timeout time.Duration) (*client.StackProfileDemoConnectionProbe, error) {
	ctx := command.Context()
	deadline := time.Now().Add(timeout)
	for probe.Status == "queued" {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return probe, withExitCode(exitWaitTimeout, fmt.Errorf("probe %s is still queued after %s; its verdict is not known yet", probe.ProbeID, timeout))
		}
		select {
		case <-ctx.Done():
			return probe, ctx.Err()
		case <-time.After(min(connectionProbePollInterval, remaining)):
		}
		next, readError := apiClient.GetStackProfileDemoConnectionProbe(ctx, profileID, workspaceID, probe.ProbeID)
		if readError != nil {
			return probe, fmt.Errorf("reading probe %s: %w", probe.ProbeID, readError)
		}
		probe = next
	}
	return probe, nil
}

// connectionProbeVerdictError is nil only for a passed verdict: failed and
// unknown both exit 1, so a probe that proved nothing never reads as passed.
func connectionProbeVerdictError(probe client.StackProfileDemoConnectionProbe) error {
	switch probe.Verdict {
	case "passed":
		return nil
	case "failed":
		return fmt.Errorf("the connection failed its probe: %s", valueOr(&probe.Reason, "no reason given"))
	default:
		return fmt.Errorf("the probe proved nothing (verdict %s, status %s): %s",
			valueOr(&probe.Verdict, "none"), probe.Status, valueOr(&probe.Reason, "no reason given"))
	}
}

func primaryEndpointLabel(connection client.ServicePackageConnection) string {
	if len(connection.Endpoints) == 0 {
		return "no endpoint"
	}
	return fmt.Sprintf("%s:%d", connection.Endpoints[0].Service, connection.Endpoints[0].Port)
}

func printConnectionProposal(out io.Writer, proposal client.StackProfileDemoConnectionProposal) {
	if !proposal.Observable {
		_, _ = fmt.Fprintf(out, "Demo %s: not observed. %s\n", proposal.DemoStatus, proposal.Reason)
		return
	}
	_, _ = fmt.Fprintf(out, "Demo %s: %d Services and %d Secrets observed.\n", proposal.DemoStatus, proposal.ObservedServices, proposal.ObservedSecrets)
	if len(proposal.Proposals) == 0 {
		_, _ = fmt.Fprintf(out, "No connection to propose. %s\n", proposal.Reason)
		return
	}
	for index, candidate := range proposal.Proposals {
		completeness := "complete"
		if !candidate.Complete {
			completeness = "incomplete"
		}
		_, _ = fmt.Fprintf(out, "\n%d. %s through %s, secret %s (%s, %s confidence)\n", index+1, candidate.Connection.Protocol,
			primaryEndpointLabel(candidate.Connection), valueOr(&candidate.Connection.SecretName, "none"), completeness, candidate.Confidence)
		if len(candidate.Unresolved) > 0 {
			_, _ = fmt.Fprintf(out, "   Unresolved: %s\n", strings.Join(candidate.Unresolved, ", "))
		}
		for _, reason := range candidate.Reasons {
			_, _ = fmt.Fprintf(out, "   - %s\n", reason)
		}
	}
	if proposal.Note != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", proposal.Note)
	}
}

func printConnectionProbe(out io.Writer, probe client.StackProfileDemoConnectionProbe) {
	_, _ = fmt.Fprintf(out, "Probe %s: %s", probe.ProbeID, probe.Status)
	if probe.Verdict != "" && probe.Status != "queued" {
		_, _ = fmt.Fprintf(out, ", verdict %s", probe.Verdict)
	}
	_, _ = fmt.Fprintln(out)
	if probe.Reason != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", probe.Reason)
	}
}
