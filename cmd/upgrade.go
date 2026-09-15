package cmd

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ankra/internal/skills"

	"github.com/spf13/cobra"
)

const (
	githubLatestReleaseAPI = "https://api.github.com/repos/ankraio/ankra-cli/releases/latest"
	githubReleasesListAPI  = "https://api.github.com/repos/ankraio/ankra-cli/releases?per_page=30"
	githubReleaseDownload  = "https://github.com/ankraio/ankra-cli/releases/download"
	installScriptURL       = "https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh"
	upgradeHTTPTimeout     = 120 * time.Second
	checksumReadLimit      = 4 << 10
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the Ankra CLI to the latest release",
	Long: `Download and install a specific Ankra CLI release, replacing the
currently running binary in place.

By default the command upgrades to the latest stable release. Install an
exact release with --version (for example --version v0.2.5, or --version
0.2.5). When a version is pinned the command installs it whether it is
newer, older or the same as the running binary, so --version doubles as a
downgrade: 'ankra upgrade --version v0.1.9' rolls back to that release.
Use --check to report whether a newer release is available without
installing anything.

When the beta channel is enabled (see 'ankra config beta enable') the
newest release including pre-releases (release candidates such as
v0.3.0-rc.1) is installed instead. Use --beta or --beta=false to override
the saved channel for a single run.

The downloaded binary is verified against its published SHA-256 checksum
before it replaces the existing executable.

The Ankra agent skills ('ankra skills install') ship inside the binary, so
an upgrade also offers to refresh the copies already installed for your
assistants; the new binary reinstalls them once it is in place, with the
same --no-rules, --no-workflows and --with-hooks choices each install was
made with. --skills takes the offer without asking and --skills=false
declines it. --yes skips only the upgrade confirmation: a scripted
'ankra upgrade --yes' leaves the installed skills as they are and says how
to refresh them; pass --yes --skills to refresh them without any question.`,
	Aliases: []string{"self-update"},
	Args:    cobra.NoArgs,
	RunE:    runUpgrade,
}

func init() {
	upgradeCmd.Flags().String("version", "", "exact release to install for an upgrade or downgrade, e.g. v0.2.5 (default: latest)")
	upgradeCmd.Flags().Bool("check", false, "check for a newer release without installing")
	upgradeCmd.Flags().BoolP("yes", "y", false, "skip the upgrade confirmation prompt (the installed agent skills are refreshed only with --skills)")
	upgradeCmd.Flags().Bool("force", false, "reinstall even if already on the target version")
	upgradeCmd.Flags().Bool("beta", false, "include pre-release versions for this run (overrides the saved channel)")
	upgradeCmd.Flags().Bool("allow-unverified", false, "install even when no SHA-256 checksum is published (insecure)")
	upgradeCmd.Flags().Bool("skills", true, "refresh the Ankra agent skills already installed for your assistants once the new binary is in place, replaying each install's options; --skills=false leaves them as they are, and without the flag you are asked (never refreshed by --yes alone)")

	setRequiresAuth(upgradeCmd, false)
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	pinnedVersion, _ := cmd.Flags().GetString("version")
	checkOnly, _ := cmd.Flags().GetBool("check")
	skipConfirm, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")
	allowUnverified, _ := cmd.Flags().GetBool("allow-unverified")

	assetName, err := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	httpClient := &http.Client{Timeout: upgradeHTTPTimeout}

	betaEnabled := betaReleasesEnabled()
	if cmd.Flags().Changed("beta") {
		betaEnabled, _ = cmd.Flags().GetBool("beta")
	}

	versionPinned := cmd.Flags().Changed("version")
	targetTag := strings.TrimSpace(pinnedVersion)
	if targetTag == "" {
		var latestTag string
		var latestErr error
		if betaEnabled {
			_, _ = fmt.Fprintln(out, "Beta channel enabled: including pre-release versions.")
			latestTag, latestErr = latestReleaseTagIncludingPrereleases(httpClient)
		} else {
			latestTag, latestErr = latestReleaseTag(httpClient)
		}
		if latestErr != nil {
			return fmt.Errorf("could not determine the latest release: %w", latestErr)
		}
		targetTag = latestTag
	} else {
		targetTag = ensureTagPrefix(targetTag)
	}

	currentVersion := normalizeVersion(version)
	targetVersion := normalizeVersion(targetTag)
	comparison := compareVersions(targetVersion, currentVersion)

	if checkOnly {
		switch {
		case comparison > 0:
			_, _ = fmt.Fprintf(out, "A newer release is available: v%s (current: v%s)\n", targetVersion, currentVersion)
			_, _ = fmt.Fprintln(out, "Run `ankra upgrade` to install it.")
		case comparison < 0:
			_, _ = fmt.Fprintf(out, "Installed version v%s is newer than v%s.\n", currentVersion, targetVersion)
		default:
			_, _ = fmt.Fprintf(out, "Already up to date (v%s).\n", currentVersion)
		}
		return nil
	}

	if comparison == 0 && !force && !versionPinned {
		_, _ = fmt.Fprintf(out, "Already up to date (v%s).\n", currentVersion)
		return nil
	}
	if comparison < 0 && !force && !versionPinned {
		_, _ = fmt.Fprintf(out,
			"Installed version v%s is newer than the requested v%s. "+
				"Pin it with --version v%s to downgrade, or use --force.\n",
			currentVersion, targetVersion, targetVersion)
		return nil
	}

	executablePath, err := currentExecutablePath()
	if err != nil {
		return err
	}
	if isHomebrewManagedPath(executablePath) {
		return fmt.Errorf(
			"this ankra binary is managed by Homebrew (%s).\n"+
				"Self-updating it would be reverted by the next `brew upgrade`. Upgrade with:\n"+
				"  brew update && brew upgrade ankra",
			executablePath)
	}

	action := "Upgrade"
	switch {
	case comparison < 0:
		action = "Downgrade"
	case comparison == 0:
		action = "Reinstall"
	}
	prompt := fmt.Sprintf("%s ankra from v%s to v%s (%s)? [y/N]: ",
		action, currentVersion, targetVersion, executablePath)
	input := bufio.NewReader(cmd.InOrStdin())
	if err := confirmPrompt(input, out, prompt, skipConfirm); err != nil {
		return err
	}

	skillsFlag, _ := cmd.Flags().GetBool("skills")
	installedSkillClients, skillRefreshGroups, detectionError := skillsInstalledClientsForUpgrade()
	if detectionError != nil && skillsFlag {
		_, _ = fmt.Fprintf(out, "Warning: could not check which assistants carry the Ankra agent skills (%v); refresh them by hand afterwards with `ankra skills install --force`.\n", detectionError)
	}
	refreshSkills, err := decideSkillsRefresh(input, out, cmd.ErrOrStderr(), skillsRefreshChoice{
		Clients:       installedSkillClients,
		Groups:        skillRefreshGroups,
		TargetVersion: targetVersion,
		FlagValue:     skillsFlag,
		FlagExplicit:  cmd.Flags().Changed("skills"),
		SkipPrompts:   skipConfirm,
	})
	if err != nil {
		return err
	}

	binaryURL, checksumURL := releaseDownloadURLs(targetTag, assetName)

	stagingDir, err := os.MkdirTemp("", "ankra-upgrade-")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(stagingDir); removeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clean up %s: %v\n", stagingDir, removeErr)
		}
	}()

	downloadedBinary := filepath.Join(stagingDir, "ankra")
	_, _ = fmt.Fprintf(out, "Downloading %s ...\n", binaryURL)
	if err := downloadToFile(httpClient, binaryURL, downloadedBinary); err != nil {
		return fmt.Errorf("download binary: %w", err)
	}

	expectedChecksum, err := fetchChecksum(httpClient, checksumURL)
	if err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	if expectedChecksum == "" {
		if !allowUnverified {
			return fmt.Errorf(
				"no SHA-256 checksum published for %s; refusing to install an unverified binary "+
					"(pass --allow-unverified to override)", targetTag)
		}
		_, _ = fmt.Fprintln(out, "Warning: no checksum published for this release; skipping verification (--allow-unverified).")
	} else {
		actualChecksum, sumErr := sha256OfFile(downloadedBinary)
		if sumErr != nil {
			return sumErr
		}
		if !strings.EqualFold(actualChecksum, expectedChecksum) {
			return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
		}
		_, _ = fmt.Fprintln(out, "Checksum verified.")
	}

	if err := replaceExecutable(executablePath, downloadedBinary); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Ankra CLI upgraded to v%s.\n", targetVersion)

	switch {
	case refreshSkills:
		refreshInstalledSkills(out, executablePath, skillRefreshGroups)
	case len(installedSkillClients) == 0 && skillsFlag && detectionError == nil:
		_, _ = fmt.Fprintln(out, "No Ankra agent skills are installed for this user; `ankra skills install` adds them to your assistants.")
	}
	return nil
}

// skillsRefreshChoice is what decideSkillsRefresh weighs: which assistants
// carry an install, how each was installed, and how the caller answered
// before being asked.
type skillsRefreshChoice struct {
	Clients []skills.Client
	// Groups are the same clients grouped by the install options recorded
	// for them, which is what the refresh replays. Empty means every client
	// refreshes with the defaults.
	Groups        []skillsRefreshGroup
	TargetVersion string
	// FlagValue is --skills; FlagExplicit says whether it was passed at all,
	// because the flag defaults to true and a default is an offer, not an
	// answer.
	FlagValue    bool
	FlagExplicit bool
	// SkipPrompts is --yes. It skips the upgrade confirmation and nothing
	// else: a script that meant "do not ask me" did not mean "write into my
	// assistants' configuration", so without --skills the offer is declined
	// and the command to run by hand is printed instead.
	SkipPrompts bool
}

// decideSkillsRefresh answers whether the installed skills are refreshed
// after the binary swap. Nothing installed means nothing to refresh and no
// question; an explicit --skills or --skills=false is the answer; --yes on
// its own declines and says so on stderr, because it only ever promised to
// skip the upgrade confirmation; otherwise the user is asked, and Enter
// means yes, because skills that lag the binary are the failure this exists
// to prevent. Input that ends before the question is answered is not Enter:
// nobody saw the question, so nothing is overwritten and the command to run
// by hand is printed instead.
func decideSkillsRefresh(in io.Reader, out, errOut io.Writer, choice skillsRefreshChoice) (bool, error) {
	if len(choice.Clients) == 0 || (choice.FlagExplicit && !choice.FlagValue) {
		return false, nil
	}
	if choice.FlagExplicit {
		return true, nil
	}
	if choice.SkipPrompts {
		_, _ = fmt.Fprintf(errOut, "The Ankra agent skills installed for %s were not refreshed: --yes skips only the upgrade confirmation. Pass --skills next time, or run: %s\n",
			clientDisplayNames(choice.Clients), skillsRefreshCommandLine("ankra", choice.refreshGroups()))
		return false, nil
	}
	_, _ = fmt.Fprintf(out, "Also refresh the Ankra agent skills installed for %s to v%s? [Y/n]: ",
		clientDisplayNames(choice.Clients), choice.TargetVersion)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	if err == io.EOF && line == "" {
		_, _ = fmt.Fprintf(out, "\nNo answer; the skills are left as they are. Refresh them afterwards with: %s\n",
			skillsRefreshCommandLine("ankra", choice.refreshGroups()))
		return false, nil
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "" || answer == "y" || answer == "yes", nil
}

// refreshGroups is what the refresh would run: the recorded groups when the
// caller resolved them, otherwise every client with the defaults.
func (choice skillsRefreshChoice) refreshGroups() []skillsRefreshGroup {
	if len(choice.Groups) > 0 {
		return choice.Groups
	}
	if len(choice.Clients) == 0 {
		return nil
	}
	return []skillsRefreshGroup{{Options: skills.DefaultInstallOptions(), Clients: choice.Clients}}
}

// clientDisplayNames renders "Claude Code", "Claude Code and Cursor" or
// "Claude Code, Cursor and Codex".
func clientDisplayNames(clients []skills.Client) string {
	names := make([]string, 0, len(clients))
	for _, client := range clients {
		names = append(names, client.DisplayName)
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// skillsInstalledClientsForUpgrade lists the assistants whose personal Ankra
// skills install the upgrade may refresh, and those same assistants grouped
// by the install options recorded for them. A failure to look is reported
// and the offer is skipped; it never fails the upgrade and is never read as
// "nothing installed".
func skillsInstalledClientsForUpgrade() ([]skills.Client, []skillsRefreshGroup, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("determine the home directory: %w", err)
	}
	clients, err := skillsInstalledClients(home)
	if err != nil {
		return nil, nil, err
	}
	groups, err := skillsRefreshGroupsFor(home, clients)
	if err != nil {
		return nil, nil, err
	}
	return clients, groups, nil
}

// skillsRefreshGroup is one `skills install` run of the refresh: the clients
// that were installed with the same options, so one command replays them.
type skillsRefreshGroup struct {
	Options skills.InstallOptions
	Clients []skills.Client
}

// skillsRefreshGroupsFor reads the options `skills install` recorded for each
// client under root and groups the clients by them, in order of first
// appearance so the output is stable. A client with nothing recorded (an
// install made before the options were recorded) joins the defaults group,
// which is what the refresh always did for everyone. A record that cannot
// be read is an error: replaying the defaults over an install whose options
// are unknown is the defect this prevents.
func skillsRefreshGroupsFor(root string, clients []skills.Client) ([]skillsRefreshGroup, error) {
	var groups []skillsRefreshGroup
	for _, client := range clients {
		options, _, err := skills.RecordedInstallOptions(root, client.ID)
		if err != nil {
			return nil, fmt.Errorf("read the recorded install options for %s: %w", client.DisplayName, err)
		}
		placed := false
		for index := range groups {
			if groups[index].Options == options {
				groups[index].Clients = append(groups[index].Clients, client)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, skillsRefreshGroup{Options: options, Clients: []skills.Client{client}})
		}
	}
	return groups, nil
}

// skillsRefreshArguments is the argument list the new binary is run with to
// reinstall the skills for one group: --force, because the point is to
// overwrite the copies the previous release installed; the group's recorded
// --no-rules, --no-workflows and --with-hooks, so a person who declined the
// rule block or asked for the hook gets exactly that again; and one --client
// per assistant that carries an install, so nothing is installed anywhere
// new.
func skillsRefreshArguments(group skillsRefreshGroup) []string {
	arguments := []string{"skills", "install", "--force"}
	if !group.Options.Rules {
		arguments = append(arguments, "--no-rules")
	}
	if !group.Options.Workflows {
		arguments = append(arguments, "--no-workflows")
	}
	if group.Options.Hooks {
		arguments = append(arguments, "--with-hooks")
	}
	for _, client := range group.Clients {
		arguments = append(arguments, "--client", client.ID)
	}
	return arguments
}

// skillsRefreshCommandLine renders the refresh as something to paste into a
// shell: one command per group, chained with &&.
func skillsRefreshCommandLine(executable string, groups []skillsRefreshGroup) string {
	commands := make([]string, 0, len(groups))
	for _, group := range groups {
		commands = append(commands, executable+" "+strings.Join(skillsRefreshArguments(group), " "))
	}
	return strings.Join(commands, " && ")
}

// runSkillsRefresh runs the freshly installed binary so the skills that land
// are the ones embedded in the target version, not the ones in this process.
// Replaced in tests.
var runSkillsRefresh = func(out io.Writer, executable string, arguments []string) error {
	command := exec.Command(executable, arguments...)
	command.Stdout = out
	command.Stderr = out
	return command.Run()
}

// refreshInstalledSkills reinstalls the skills through the new binary, one
// run per group of clients that share install options. A failure is reported
// with the command to run by hand and does not fail the upgrade: the binary
// is already replaced, and saying so is more useful than a non-zero exit
// that reads as a failed upgrade. A failed group does not stop the others.
func refreshInstalledSkills(out io.Writer, executable string, groups []skillsRefreshGroup) {
	all := make([]skills.Client, 0)
	for _, group := range groups {
		all = append(all, group.Clients...)
	}
	_, _ = fmt.Fprintf(out, "Refreshing the Ankra agent skills for %s ...\n", clientDisplayNames(all))
	for _, group := range groups {
		arguments := skillsRefreshArguments(group)
		if err := runSkillsRefresh(out, executable, arguments); err != nil {
			_, _ = fmt.Fprintf(out, "Warning: the agent skills for %s were not refreshed: %v\nRun it by hand: %s %s\n",
				clientDisplayNames(group.Clients), err, executable, strings.Join(arguments, " "))
		}
	}
}

// releaseAssetName maps the Go runtime OS/arch onto the published release
// asset name (for example darwin/arm64 -> ankra-cli-darwin-arm64). It mirrors
// the matrix in install.sh; only the OS/arch pairs that ship a binary are
// accepted.
func releaseAssetName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux":
	default:
		return "", fmt.Errorf(
			"unsupported operating system %q: `ankra upgrade` supports darwin and linux. Reinstall manually from https://github.com/ankraio/ankra-cli/releases",
			goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf(
			"unsupported architecture %q: `ankra upgrade` supports amd64 and arm64",
			goarch)
	}
	return fmt.Sprintf("ankra-cli-%s-%s", goos, goarch), nil
}

// releaseDownloadURLs returns the binary and checksum URLs for a given release
// tag and asset. The tag is used verbatim so a pinned `--version v0.2.5`
// resolves to the matching GitHub release path.
func releaseDownloadURLs(tag, asset string) (binaryURL, checksumURL string) {
	binaryURL = fmt.Sprintf("%s/%s/%s", githubReleaseDownload, tag, asset)
	checksumURL = binaryURL + ".sha256"
	return binaryURL, checksumURL
}

func latestReleaseTag(httpClient *http.Client) (string, error) {
	request, err := http.NewRequest(http.MethodGet, githubLatestReleaseAPI, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")

	response, err := httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s", response.StatusCode, githubLatestReleaseAPI)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode release response: %w", err)
	}
	if strings.TrimSpace(payload.TagName) == "" {
		return "", errors.New("release response did not include a tag_name")
	}
	return payload.TagName, nil
}

// latestReleaseTagIncludingPrereleases returns the newest published release
// tag, including pre-releases (release candidates). GitHub returns the list
// sorted newest first, so the first non-draft entry is the latest version on
// the beta channel.
func latestReleaseTagIncludingPrereleases(httpClient *http.Client) (string, error) {
	request, err := http.NewRequest(http.MethodGet, githubReleasesListAPI, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")

	response, err := httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s", response.StatusCode, githubReleasesListAPI)
	}

	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(response.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("decode releases response: %w", err)
	}

	for _, release := range releases {
		if release.Draft {
			continue
		}
		if tag := strings.TrimSpace(release.TagName); tag != "" {
			return tag, nil
		}
	}
	return "", errors.New("no published releases found")
}

func downloadToFile(httpClient *http.Client, url, destination string) error {
	response, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d for %s", response.StatusCode, url)
	}

	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// fetchChecksum downloads the .sha256 file for a release asset and returns the
// hex digest. A 404 yields an empty string (older releases predate published
// checksums), matching the lenient behaviour of install.sh.
func fetchChecksum(httpClient *http.Client, url string) (string, error) {
	response, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d for %s", response.StatusCode, url)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, checksumReadLimit))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", errors.New("checksum file is empty")
	}
	return fields[0], nil
}

func sha256OfFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// isHomebrewManagedPath reports whether a resolved executable path lives in a
// Homebrew Cellar, meaning Homebrew owns the file. Self-updating such a binary
// would desynchronise it from the recorded formula version and be undone by
// the next `brew upgrade`, so `ankra upgrade` refuses and defers to brew. The
// path must already have symlinks resolved (currentExecutablePath does this):
// brew installs `<prefix>/bin/ankra` as a symlink into the Cellar, while a
// manually copied binary under `<prefix>/bin` resolves outside it and stays
// self-updatable.
func isHomebrewManagedPath(executablePath string) bool {
	normalized := filepath.ToSlash(executablePath)
	return strings.Contains(normalized, "/Cellar/")
}

// currentExecutablePath resolves the path of the running binary, following any
// symlinks so the upgrade replaces the real file rather than a link.
func currentExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return executable, nil
	}
	return resolved, nil
}

// replaceExecutable atomically swaps the running binary for the freshly
// downloaded one. The replacement is staged in the destination directory so
// the final rename stays on the same filesystem (os.Rename cannot cross
// devices), giving an all-or-nothing swap.
func replaceExecutable(executablePath, newBinaryPath string) error {
	destinationDir := filepath.Dir(executablePath)

	staged, err := os.CreateTemp(destinationDir, ".ankra-upgrade-*")
	if err != nil {
		if os.IsPermission(err) {
			return permissionDeniedError(destinationDir, executablePath)
		}
		return fmt.Errorf("stage new binary in %s: %w", destinationDir, err)
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()

	source, err := os.Open(newBinaryPath)
	if err != nil {
		_ = staged.Close()
		return err
	}
	defer func() { _ = source.Close() }()

	if _, err := io.Copy(staged, source); err != nil {
		_ = staged.Close()
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return err
	}

	if err := os.Rename(stagedPath, executablePath); err != nil {
		if os.IsPermission(err) {
			return permissionDeniedError(destinationDir, executablePath)
		}
		return fmt.Errorf("replace %s: %w", executablePath, err)
	}
	return nil
}

func permissionDeniedError(destinationDir, executablePath string) error {
	return fmt.Errorf(
		"permission denied writing to %s.\n"+
			"The Ankra CLI at %s is not writable by the current user.\n"+
			"Re-run with elevated privileges (sudo ankra upgrade) or reinstall with:\n"+
			"  bash <(curl -sL %s)",
		destinationDir, executablePath, installScriptURL)
}

// normalizeVersion strips a leading v/V and surrounding whitespace so build
// versions ("0.2.4") and release tags ("v0.2.4") compare cleanly.
func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	value = strings.TrimPrefix(value, "V")
	return value
}

// ensureTagPrefix normalises a user-supplied release version into the tag form
// used by GitHub releases (a leading "v"), so `--version 0.2.5` and
// `--version v0.2.5` both resolve to the same release.
func ensureTagPrefix(value string) string {
	normalized := normalizeVersion(value)
	if normalized == "" {
		return ""
	}
	return "v" + normalized
}

// compareVersions compares two versions using semantic-versioning precedence,
// returning -1, 0 or 1 when left is older, equal or newer than right. The
// numeric core (major.minor.patch) is compared first; when the cores are
// equal, a release with no pre-release outranks one with a pre-release (so
// 0.3.0 is newer than 0.3.0-rc.1), and pre-release identifiers are compared
// left to right.
func compareVersions(left, right string) int {
	leftCore, leftPrerelease := splitPrerelease(left)
	rightCore, rightPrerelease := splitPrerelease(right)

	if coreComparison := compareCoreVersions(leftCore, rightCore); coreComparison != 0 {
		return coreComparison
	}
	return comparePrerelease(leftPrerelease, rightPrerelease)
}

func splitPrerelease(value string) (core string, prerelease string) {
	if dash := strings.IndexByte(value, '-'); dash >= 0 {
		return value[:dash], value[dash+1:]
	}
	return value, ""
}

func compareCoreVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")

	segments := len(leftParts)
	if len(rightParts) > segments {
		segments = len(rightParts)
	}
	for index := 0; index < segments; index++ {
		leftSegment := numericSegment(leftParts, index)
		rightSegment := numericSegment(rightParts, index)
		if leftSegment < rightSegment {
			return -1
		}
		if leftSegment > rightSegment {
			return 1
		}
	}
	return 0
}

func numericSegment(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	number, err := strconv.Atoi(parts[index])
	if err != nil {
		return 0
	}
	return number
}

// comparePrerelease applies semver pre-release precedence. An empty
// pre-release (a stable release) outranks any pre-release. Otherwise the
// dot-separated identifiers are compared left to right: numeric identifiers
// compare numerically and rank below alphanumeric ones, and a longer set of
// identifiers wins when all shared identifiers are equal.
func comparePrerelease(left, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}

	leftIdentifiers := strings.Split(left, ".")
	rightIdentifiers := strings.Split(right, ".")

	shared := len(leftIdentifiers)
	if len(rightIdentifiers) < shared {
		shared = len(rightIdentifiers)
	}
	for index := 0; index < shared; index++ {
		if comparison := comparePrereleaseIdentifier(leftIdentifiers[index], rightIdentifiers[index]); comparison != 0 {
			return comparison
		}
	}
	switch {
	case len(leftIdentifiers) < len(rightIdentifiers):
		return -1
	case len(leftIdentifiers) > len(rightIdentifiers):
		return 1
	default:
		return 0
	}
}

func comparePrereleaseIdentifier(left, right string) int {
	leftNumber, leftErr := strconv.Atoi(left)
	rightNumber, rightErr := strconv.Atoi(right)
	switch {
	case leftErr == nil && rightErr == nil:
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
		return 0
	case leftErr == nil:
		return -1
	case rightErr == nil:
		return 1
	default:
		return strings.Compare(left, right)
	}
}
