package cmd

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	defaultHostAgentReleaseURL     = "https://github.com/ankraio/agent/releases"
	envHostAgentReleaseURL         = "ANKRA_HOST_AGENT_RELEASE_URL"
	hostAgentChecksumsAsset        = "SHA256SUMS"
	hostAgentUnitName              = "ankra-host-agent.service"
	hostAgentDownloadTimeout       = 5 * time.Minute
	hostAgentMaximumBinaryBytes    = 256 << 20
	hostAgentMaximumChecksumsBytes = 64 << 10
	hostAgentMaximumJoinTokenBytes = 4 << 10
)

// hostAgentHost is everything `targets register` touches on the machine it
// runs on, so tests can drive the whole flow without root, systemd or the
// network.
type hostAgentHost struct {
	operatingSystem string
	architecture    string
	effectiveUserID func() int
	httpClient      *http.Client
	runCommand      func(ctx context.Context, invocation hostCommandInvocation) error
	binaryPath      string
	unitPath        string
}

// hostCommandInvocation is one process `targets register` starts.
type hostCommandInvocation struct {
	path        string
	arguments   []string
	environment []string
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
}

// currentHostAgentHost returns the real host. Tests replace it.
var currentHostAgentHost = func() hostAgentHost {
	return hostAgentHost{
		operatingSystem: runtime.GOOS,
		architecture:    runtime.GOARCH,
		effectiveUserID: os.Geteuid,
		httpClient:      &http.Client{Timeout: hostAgentDownloadTimeout},
		runCommand:      runHostCommand,
		binaryPath:      "/usr/local/bin/ankra-host-agent",
		unitPath:        "/etc/systemd/system/" + hostAgentUnitName,
	}
}

func runHostCommand(ctx context.Context, invocation hostCommandInvocation) error {
	process := exec.CommandContext(ctx, invocation.path, invocation.arguments...)
	process.Env = append(os.Environ(), invocation.environment...)
	process.Stdin = invocation.stdin
	process.Stdout = invocation.stdout
	process.Stderr = invocation.stderr
	return process.Run()
}

// verifyHostAgentReleaseSignature is where the release signature check plugs
// in once the agent's host binaries are signed; until then the SHA256SUMS
// check is the only verification and this accepts every release.
var verifyHostAgentReleaseSignature = func(_ context.Context, _ hostAgentRelease) error {
	return nil
}

// hostAgentRelease names the release files one host needs.
type hostAgentRelease struct {
	Version       string `json:"version" yaml:"version"`
	BinaryAsset   string `json:"binary_asset" yaml:"binary_asset"`
	BinaryURL     string `json:"binary_url" yaml:"binary_url"`
	UnitURL       string `json:"unit_url" yaml:"unit_url"`
	ChecksumsURL  string `json:"checksums_url" yaml:"checksums_url"`
	SignatureNote string `json:"signature,omitempty" yaml:"signature,omitempty"`
}

// hostAgentRegistration is what `targets register` did, or with
// --no-install would do.
type hostAgentRegistration struct {
	DryRun       bool              `json:"dry_run" yaml:"dry_run"`
	Environment  string            `json:"environment" yaml:"environment"`
	Name         string            `json:"name" yaml:"name"`
	Labels       map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	AnkraURL     string            `json:"ankra_url" yaml:"ankra_url"`
	Release      hostAgentRelease  `json:"release" yaml:"release"`
	BinarySHA256 string            `json:"binary_sha256,omitempty" yaml:"binary_sha256,omitempty"`
	UnitSHA256   string            `json:"unit_sha256,omitempty" yaml:"unit_sha256,omitempty"`
	BinaryPath   string            `json:"binary_path" yaml:"binary_path"`
	UnitPath     string            `json:"unit_path" yaml:"unit_path"`
	Steps        []string          `json:"steps" yaml:"steps"`
}

func newTargetsRegisterCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "register",
		Short: "Register this machine as a host deploy target (run as root on the host)",
		Long: `Register this machine as a host deploy target and start its agent. Run it
as root on the host itself, with a join token from 'ankra targets join-token
create' on stdin.

The command:

  1. downloads ankra-host-agent for this machine's OS and architecture and its
     systemd unit from the agent release, and refuses them unless their
     SHA-256 matches the release's SHA256SUMS;
  2. installs the binary as /usr/local/bin/ankra-host-agent;
  3. runs 'ankra-host-agent register', passing the join token on stdin - the
     agent does the registration and stores its own identity token;
  4. installs and starts ankra-host-agent.service.

The CLI never sends the join token anywhere itself and needs no 'ankra login'
on the host. --no-install prints these steps without doing any of them.

The agent reaches the platform at --base-url (or the configured base URL),
over outbound HTTPS only.`,
		Example: `  ankra targets join-token create --environment production | \
    ssh root@web-1 ankra targets register --environment production --name web-1 --label role=web --token-stdin

  ankra targets register --environment production --name web-1 --token-stdin --no-install`,
		Args: cobra.NoArgs,
		RunE: runTargetsRegister,
	}
	command.Flags().String("environment", "", "Environment to register into (required)")
	command.Flags().String("name", "", "Name of this host target, unique in the environment (required)")
	command.Flags().StringArray("label", nil, "Label as key=value; repeat for more (deploy stages select targets by label)")
	command.Flags().Bool("token-stdin", false, "Read the join token from stdin (required; the token is never taken as a flag)")
	command.Flags().Bool("no-install", false, "Print what would be downloaded, run and installed, and do nothing")
	command.Flags().String("agent-version", "latest", "Agent release to install, e.g. v2.1.40")
	command.Flags().String("release-url", "", "Base URL of the agent releases (default "+defaultHostAgentReleaseURL+", or $"+envHostAgentReleaseURL+")")
	_ = command.MarkFlagRequired("environment")
	_ = command.MarkFlagRequired("name")
	registerStructuredOutputFlags(command)
	setRequiresAuth(command, false)
	return command
}

func runTargetsRegister(command *cobra.Command, _ []string) error {
	environmentName, _ := command.Flags().GetString("environment")
	targetName, _ := command.Flags().GetString("name")
	labelEntries, _ := command.Flags().GetStringArray("label")
	isTokenOnStdin, _ := command.Flags().GetBool("token-stdin")
	isDryRun, _ := command.Flags().GetBool("no-install")
	agentVersion, _ := command.Flags().GetString("agent-version")

	if validationError := validateEnvironmentName(environmentName); validationError != nil {
		return validationError
	}
	if !hostTargetNamePattern.MatchString(targetName) {
		return withExitCode(exitUsage, fmt.Errorf(
			"--name %q is not a valid host target name: use letters, digits, '.', '_' and '-', "+
				"starting with a letter or digit, at most 63 characters", targetName))
	}
	labels, labelError := parseHostTargetLabels(labelEntries)
	if labelError != nil {
		return labelError
	}
	if !isTokenOnStdin {
		return withExitCode(exitUsage, errors.New(
			"--token-stdin is required: pipe the join token from 'ankra targets join-token create' into this command"))
	}
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	ankraURL, urlError := hostAgentPlatformURL(command)
	if urlError != nil {
		return urlError
	}

	host := currentHostAgentHost()
	release, releaseError := resolveHostAgentRelease(command, host, agentVersion)
	if releaseError != nil {
		return releaseError
	}
	registration := hostAgentRegistration{
		DryRun:      isDryRun,
		Environment: environmentName,
		Name:        targetName,
		Labels:      labels,
		AnkraURL:    ankraURL,
		Release:     release,
		BinaryPath:  host.binaryPath,
		UnitPath:    host.unitPath,
	}
	registerArguments := hostAgentRegisterArguments(environmentName, targetName, labelEntries)
	registration.Steps = []string{
		"download " + release.BinaryURL,
		"download " + release.UnitURL,
		"verify both against the SHA-256 sums in " + release.ChecksumsURL,
		"install " + host.binaryPath + " (mode 0755)",
		fmt.Sprintf("run ANKRA_URL=%s %s %s (join token on stdin)", ankraURL, host.binaryPath, strings.Join(registerArguments, " ")),
		"install " + host.unitPath + " (mode 0644)",
		"run systemctl daemon-reload",
		"run systemctl enable --now " + hostAgentUnitName,
	}

	if isDryRun {
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, registration)
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Would register this host as %s in environment %s (nothing was changed):\n", targetName, environmentName)
		for index, step := range registration.Steps {
			_, _ = fmt.Fprintf(out, "  %d. %s\n", index+1, step)
		}
		return nil
	}

	if userID := host.effectiveUserID(); userID != 0 {
		return withExitCode(exitError, fmt.Errorf(
			"ankra targets register must run as root on the host (running as uid %d): it installs %s and a systemd unit. "+
				"Re-run it with sudo, or add --no-install to see what it would do", userID, host.binaryPath))
	}
	joinToken, tokenError := readJoinToken(command.InOrStdin())
	if tokenError != nil {
		return tokenError
	}

	progress := command.ErrOrStderr()
	agentOutput := command.OutOrStdout()
	if format != outputDefault {
		agentOutput = progress
	}
	ctx := command.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	downloadDirectory, directoryError := os.MkdirTemp("", "ankra-host-agent-")
	if directoryError != nil {
		return fmt.Errorf("create download directory: %w", directoryError)
	}
	defer func() { _ = os.RemoveAll(downloadDirectory) }()

	_, _ = fmt.Fprintf(progress, "Downloading ankra-host-agent %s (%s)...\n", release.Version, release.BinaryAsset)
	downloadedBinary := filepath.Join(downloadDirectory, release.BinaryAsset)
	downloadedUnit := filepath.Join(downloadDirectory, hostAgentUnitName)
	checksums, checksumsError := fetchHostAgentChecksums(ctx, host.httpClient, release.ChecksumsURL)
	if checksumsError != nil {
		return checksumsError
	}
	binaryDigest, binaryError := downloadVerifiedHostAgentAsset(ctx, host.httpClient, release.BinaryURL,
		release.BinaryAsset, downloadedBinary, checksums)
	if binaryError != nil {
		return binaryError
	}
	unitDigest, unitError := downloadVerifiedHostAgentAsset(ctx, host.httpClient, release.UnitURL,
		hostAgentUnitName, downloadedUnit, checksums)
	if unitError != nil {
		return unitError
	}
	if signatureError := verifyHostAgentReleaseSignature(ctx, release); signatureError != nil {
		return fmt.Errorf("refusing the ankra-host-agent release: %w", signatureError)
	}
	registration.BinarySHA256 = binaryDigest
	registration.UnitSHA256 = unitDigest
	_, _ = fmt.Fprintf(progress, "Verified %s (sha256 %s) and %s against %s.\n",
		release.BinaryAsset, binaryDigest, hostAgentUnitName, hostAgentChecksumsAsset)

	if installError := installHostFile(downloadedBinary, host.binaryPath, 0o755); installError != nil {
		return installError
	}
	_, _ = fmt.Fprintf(progress, "Installed %s.\n", host.binaryPath)

	registerError := host.runCommand(ctx, hostCommandInvocation{
		path:        host.binaryPath,
		arguments:   registerArguments,
		environment: []string{"ANKRA_URL=" + ankraURL},
		stdin:       strings.NewReader(joinToken + "\n"),
		stdout:      agentOutput,
		stderr:      progress,
	})
	if registerError != nil {
		return fmt.Errorf("ankra-host-agent register failed, so the service was not installed: %w", registerError)
	}

	if installError := installHostFile(downloadedUnit, host.unitPath, 0o644); installError != nil {
		return installError
	}
	for _, systemctlArguments := range [][]string{{"daemon-reload"}, {"enable", "--now", hostAgentUnitName}} {
		systemctlError := host.runCommand(ctx, hostCommandInvocation{
			path:      "systemctl",
			arguments: systemctlArguments,
			stdout:    progress,
			stderr:    progress,
		})
		if systemctlError != nil {
			return fmt.Errorf("systemctl %s failed: %w", strings.Join(systemctlArguments, " "), systemctlError)
		}
	}

	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, registration)
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(),
		"Registered %s in environment %s and started %s.\nCheck it with: ankra targets get %s\n",
		targetName, environmentName, hostAgentUnitName, targetName)
	return nil
}

// parseHostTargetLabels turns repeated --label key=value flags into a map,
// refusing an entry without a key or a key given twice.
func parseHostTargetLabels(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	labels := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, hasSeparator := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !hasSeparator || key == "" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--label %q must be key=value", entry))
		}
		if _, duplicate := labels[key]; duplicate {
			return nil, withExitCode(exitUsage, fmt.Errorf("--label %q is given twice", key))
		}
		labels[key] = value
	}
	return labels, nil
}

// hostAgentRegisterArguments is the agent's register invocation: the same
// flags this command took, with the join token following on stdin.
func hostAgentRegisterArguments(environmentName string, targetName string, labelEntries []string) []string {
	arguments := []string{"register", "--environment", environmentName, "--name", targetName}
	sortedLabels := append([]string{}, labelEntries...)
	sort.Strings(sortedLabels)
	for _, entry := range sortedLabels {
		arguments = append(arguments, "--label", entry)
	}
	return append(arguments, "--token-stdin")
}

// hostAgentPlatformURL is the platform the agent registers with: --base-url,
// else ANKRA_BASE_URL, else the base URL a saved login uses, else production.
// The host usually has no login, so the environment comes before the saved
// file here.
func hostAgentPlatformURL(command *cobra.Command) (string, error) {
	rawURL, isFlagSet := flagValue(command.Root().PersistentFlags().Lookup("base-url"))
	if !isFlagSet || rawURL == "" {
		rawURL = os.Getenv(envAnkraBaseURL)
	}
	if rawURL == "" {
		_, rawURL = readSavedCredentials()
	}
	if rawURL == "" {
		rawURL = defaultBaseURL
	}
	normalizedURL, normalizeError := client.NormalizeBaseURL(rawURL, os.Getenv(envAllowInsecureHTTP) == "1")
	if normalizeError != nil {
		return "", withExitCode(exitUsage, fmt.Errorf("platform URL: %w", normalizeError))
	}
	return normalizedURL, nil
}

// resolveHostAgentRelease names the release files for this host. The host
// agent is published for Linux on amd64 and arm64 only.
func resolveHostAgentRelease(command *cobra.Command, host hostAgentHost, agentVersion string) (hostAgentRelease, error) {
	if host.operatingSystem != "linux" {
		return hostAgentRelease{}, withExitCode(exitUsage, fmt.Errorf(
			"ankra-host-agent runs on Linux only, and this machine runs %s: run 'ankra targets register' on the host itself",
			host.operatingSystem))
	}
	switch host.architecture {
	case "amd64", "arm64":
	default:
		return hostAgentRelease{}, withExitCode(exitUsage, fmt.Errorf(
			"ankra-host-agent is published for amd64 and arm64, not %s", host.architecture))
	}
	releaseURL, _ := command.Flags().GetString("release-url")
	if releaseURL == "" {
		releaseURL = os.Getenv(envHostAgentReleaseURL)
	}
	if releaseURL == "" {
		releaseURL = defaultHostAgentReleaseURL
	}
	releaseURL = strings.TrimRight(releaseURL, "/")
	agentVersion = strings.TrimSpace(agentVersion)
	if agentVersion == "" {
		agentVersion = "latest"
	}
	downloadBase := releaseURL + "/latest/download"
	if agentVersion != "latest" {
		agentVersion = ensureTagPrefix(agentVersion)
		downloadBase = releaseURL + "/download/" + agentVersion
	}
	binaryAsset := fmt.Sprintf("ankra-host-agent-%s-%s", host.operatingSystem, host.architecture)
	return hostAgentRelease{
		Version:       agentVersion,
		BinaryAsset:   binaryAsset,
		BinaryURL:     downloadBase + "/" + binaryAsset,
		UnitURL:       downloadBase + "/" + hostAgentUnitName,
		ChecksumsURL:  downloadBase + "/" + hostAgentChecksumsAsset,
		SignatureNote: "not yet published for host agent releases; SHA256SUMS is checked",
	}, nil
}

// readJoinToken reads the join token from the first non-empty line of stdin.
func readJoinToken(stdin io.Reader) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(stdin, hostAgentMaximumJoinTokenBytes))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			return line, nil
		}
	}
	if scanError := scanner.Err(); scanError != nil {
		return "", fmt.Errorf("read the join token from stdin: %w", scanError)
	}
	return "", withExitCode(exitUsage, errors.New(
		"no join token on stdin: pipe the token from 'ankra targets join-token create --environment <env>'"))
}

// fetchHostAgentChecksums downloads and parses the release's SHA256SUMS
// ("<hex>  <asset>" per line, the sha256sum format).
func fetchHostAgentChecksums(ctx context.Context, httpClient *http.Client, checksumsURL string) (map[string]string, error) {
	body, fetchError := fetchHostAgentAsset(ctx, httpClient, checksumsURL, hostAgentMaximumChecksumsBytes)
	if fetchError != nil {
		return nil, fmt.Errorf("download %s: %w", hostAgentChecksumsAsset, fetchError)
	}
	checksums := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		checksums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("%s at %s lists no checksums; refusing an unverifiable release", hostAgentChecksumsAsset, checksumsURL)
	}
	return checksums, nil
}

// downloadVerifiedHostAgentAsset downloads one asset to destination and
// refuses it unless its SHA-256 is the one SHA256SUMS lists for it.
func downloadVerifiedHostAgentAsset(ctx context.Context, httpClient *http.Client, assetURL string, assetName string,
	destination string, checksums map[string]string) (string, error) {
	expectedDigest, isListed := checksums[assetName]
	if !isListed {
		return "", fmt.Errorf("%s lists no checksum for %s; refusing an unverifiable release", hostAgentChecksumsAsset, assetName)
	}
	body, fetchError := fetchHostAgentAsset(ctx, httpClient, assetURL, hostAgentMaximumBinaryBytes)
	if fetchError != nil {
		return "", fmt.Errorf("download %s: %w", assetName, fetchError)
	}
	digest := sha256.Sum256(body)
	actualDigest := hex.EncodeToString(digest[:])
	if actualDigest != expectedDigest {
		return "", fmt.Errorf("%s has sha256 %s but %s lists %s; refusing it", assetName, actualDigest, hostAgentChecksumsAsset, expectedDigest)
	}
	if writeError := os.WriteFile(destination, body, 0o600); writeError != nil {
		return "", fmt.Errorf("save %s: %w", assetName, writeError)
	}
	return actualDigest, nil
}

func fetchHostAgentAsset(ctx context.Context, httpClient *http.Client, assetURL string, maximumBytes int64) ([]byte, error) {
	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if requestError != nil {
		return nil, requestError
	}
	response, responseError := httpClient.Do(request)
	if responseError != nil {
		return nil, responseError
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", response.StatusCode, assetURL)
	}
	body, readError := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if readError != nil {
		return nil, readError
	}
	if int64(len(body)) > maximumBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", assetURL, maximumBytes)
	}
	return body, nil
}

// installHostFile copies source over destination atomically: a temporary
// file beside the destination, then a rename.
func installHostFile(source string, destination string, mode os.FileMode) error {
	content, readError := os.ReadFile(source)
	if readError != nil {
		return fmt.Errorf("read %s: %w", source, readError)
	}
	if directoryError := os.MkdirAll(filepath.Dir(destination), 0o755); directoryError != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(destination), directoryError)
	}
	temporaryPath := destination + ".ankra-new"
	if writeError := os.WriteFile(temporaryPath, content, mode); writeError != nil {
		return fmt.Errorf("install %s: %w", destination, writeError)
	}
	if chmodError := os.Chmod(temporaryPath, mode); chmodError != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("install %s: %w", destination, chmodError)
	}
	if renameError := os.Rename(temporaryPath, destination); renameError != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("install %s: %w", destination, renameError)
	}
	return nil
}
