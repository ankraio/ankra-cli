package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/migrate"
)

// Installing a module is fetching one executable into ~/.ankra/modules and
// asking it to describe itself. There is no index and no registration: the
// URL (or file) is the distribution, the describe answer is the handshake.

var (
	migrateModulesInstallName   string
	migrateModulesInstallSHA    string
	migrateModulesInstallForce  bool
	migrateModulesInstallYes    bool
	migrateModulesUninstallYes  bool
	migrateModuleNamePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	maximumModuleDownloadBytes  = int64(256 << 20)
	moduleDownloadClientTimeout = 10 * time.Minute
)

var migrateModulesInstallCmd = &cobra.Command{
	Use:   "install <url-or-file>",
	Short: "Install a migrate module into ~/.ankra/modules",
	Long: `Fetch one module executable - from an https URL or a local file - into
~/.ankra/modules, where 'ankra migrate' discovers it without touching PATH.

Before anything is kept, the module's describe verb is run and its answer
checked: the protocol version, and the name the module calls itself, which
is the name it is installed under. A module runs with your permissions, on
your files and your Docker socket; install only modules you trust, and pass
--sha256 when the author publishes a checksum.`,
	Example: `  ankra migrate modules install https://github.com/org/x/releases/download/v1/ankra-module-procfile
  ankra migrate modules install ./ankra-module-procfile --yes
  ankra migrate modules install https://example.com/m --sha256 9f86d081...`,
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{annotationRequiresAuth: "false"},
	RunE:        runMigrateModulesInstall,
}

var migrateModulesUninstallCmd = &cobra.Command{
	Use:   "uninstall <name>",
	Short: "Remove a module installed in ~/.ankra/modules",
	Long: `Delete an installed module's executable from ~/.ankra/modules. A module
found on PATH is managed by whatever put it there and is refused with its
location, so nothing outside the CLI's own directory is ever removed.`,
	Example:     `  ankra migrate modules uninstall procfile --yes`,
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{annotationRequiresAuth: "false"},
	RunE:        runMigrateModulesUninstall,
}

func init() {
	migrateModulesInstallCmd.Flags().StringVar(&migrateModulesInstallName, "name", "", "Expected module name; refused when the module calls itself something else")
	migrateModulesInstallCmd.Flags().StringVar(&migrateModulesInstallSHA, "sha256", "", "Hex sha256 the download must match")
	migrateModulesInstallCmd.Flags().BoolVar(&migrateModulesInstallForce, "force", false, "Replace a module of the same name that is already installed")
	migrateModulesInstallCmd.Flags().BoolVarP(&migrateModulesInstallYes, "yes", "y", false, "Skip the confirmation prompt")
	migrateModulesCmd.AddCommand(migrateModulesInstallCmd)

	migrateModulesUninstallCmd.Flags().BoolVarP(&migrateModulesUninstallYes, "yes", "y", false, "Skip the confirmation prompt")
	migrateModulesCmd.AddCommand(migrateModulesUninstallCmd)
}

func runMigrateModulesInstall(cmd *cobra.Command, args []string) error {
	source := args[0]
	content, fetchError := fetchModuleContent(source)
	if fetchError != nil {
		return fetchError
	}
	digest := sha256.Sum256(content)
	digestHex := hex.EncodeToString(digest[:])
	if migrateModulesInstallSHA != "" && !strings.EqualFold(migrateModulesInstallSHA, digestHex) {
		return fmt.Errorf("the download's sha256 is %s, not the %s you required; refusing it", digestHex, strings.ToLower(migrateModulesInstallSHA))
	}

	message := fmt.Sprintf("Install %s as a migrate module? Its describe verb runs now, and the module later runs with your permissions. [y/N] ", source)
	if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), message, migrateModulesInstallYes); confirmError != nil {
		return confirmError
	}

	staging, stagingError := writeModuleStaging(content)
	if stagingError != nil {
		return stagingError
	}
	defer func() { _ = os.Remove(staging) }()

	description, describeError := migrate.DescribeExecutable(cmd.Context(), staging)
	if describeError != nil {
		return fmt.Errorf("%s does not answer describe as a migrate module: %w", source, describeError)
	}
	if !migrateModuleNamePattern.MatchString(description.Name) {
		return fmt.Errorf("the module calls itself %q, which is not a usable module name (lower-case letters, digits and dashes)", description.Name)
	}
	if migrateModulesInstallName != "" && migrateModulesInstallName != description.Name {
		return withExitCode(exitUsage, fmt.Errorf("the module calls itself %q, not %q; drop --name or fetch the module you meant", description.Name, migrateModulesInstallName))
	}

	directory, directoryError := migrate.HomeModulesDir()
	if directoryError != nil {
		return directoryError
	}
	if mkdirError := os.MkdirAll(directory, 0o755); mkdirError != nil {
		return mkdirError
	}
	destination := filepath.Join(directory, migrate.ExternalModulePrefix+description.Name)
	if _, statError := os.Stat(destination); statError == nil && !migrateModulesInstallForce {
		return withExitCode(exitUsage, fmt.Errorf("module %s is already installed at %s; pass --force to replace it", description.Name, destination))
	}
	if renameError := os.Rename(staging, destination); renameError != nil {
		return renameError
	}

	summary := struct {
		Name    string `json:"name" yaml:"name"`
		Version string `json:"version" yaml:"version"`
		Summary string `json:"summary" yaml:"summary"`
		Path    string `json:"path" yaml:"path"`
		SHA256  string `json:"sha256" yaml:"sha256"`
	}{description.Name, description.Version, description.Summary, destination, digestHex}
	if handled, err := renderStructured(cmd, summary); handled || err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed module %s %s to %s (sha256 %s).\n", description.Name, description.Version, destination, digestHex)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "See it with `ankra migrate modules`; try it with `ankra migrate detect` in a source directory.\n")
	return nil
}

// fetchModuleContent reads the module from an https URL or a local file.
// Plain http would let the network hand you an executable; it is refused.
func fetchModuleContent(source string) ([]byte, error) {
	if strings.HasPrefix(source, "http://") {
		return nil, withExitCode(exitUsage, errors.New("modules are executables; fetch them over https, not http"))
	}
	if strings.HasPrefix(source, "https://") {
		response, getError := newModuleDownloadClient().Get(source)
		if getError != nil {
			return nil, fmt.Errorf("downloading %s: %w", source, getError)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("downloading %s: the server answered status %d", source, response.StatusCode)
		}
		content, readError := io.ReadAll(io.LimitReader(response.Body, maximumModuleDownloadBytes+1))
		if readError != nil {
			return nil, fmt.Errorf("downloading %s: %w", source, readError)
		}
		if int64(len(content)) > maximumModuleDownloadBytes {
			return nil, fmt.Errorf("%s is larger than %d bytes, which no module should be", source, maximumModuleDownloadBytes)
		}
		return content, nil
	}
	content, readError := os.ReadFile(source)
	if readError != nil {
		return nil, readError
	}
	return content, nil
}

// newModuleDownloadClient builds the plain client a module download uses;
// tests substitute one that trusts their server.
var newModuleDownloadClient = func() *http.Client {
	return &http.Client{Timeout: moduleDownloadClientTimeout}
}

// writeModuleStaging puts the content in an executable temporary file, so
// describe can run before anything lands in the modules directory.
func writeModuleStaging(content []byte) (string, error) {
	staging, createError := os.CreateTemp("", "ankra-module-staging-*")
	if createError != nil {
		return "", createError
	}
	path := staging.Name()
	_, writeError := staging.Write(content)
	closeError := staging.Close()
	if writeError != nil {
		return "", writeError
	}
	if closeError != nil {
		return "", closeError
	}
	if chmodError := os.Chmod(path, 0o755); chmodError != nil {
		return "", chmodError
	}
	return path, nil
}

func runMigrateModulesUninstall(cmd *cobra.Command, args []string) error {
	name := args[0]
	directory, directoryError := migrate.HomeModulesDir()
	if directoryError != nil {
		return directoryError
	}
	installed := filepath.Join(directory, migrate.ExternalModulePrefix+name)
	if _, statError := os.Stat(installed); statError != nil {
		if onPath, lookError := lookupModuleOnPath(name); lookError == nil {
			return withExitCode(exitUsage, fmt.Errorf("module %s lives at %s, outside ~/.ankra/modules; remove it the way it was installed", name, onPath))
		}
		return withExitCode(exitNotFound, fmt.Errorf("no module named %s is installed in %s", name, directory))
	}
	message := fmt.Sprintf("Remove module %s (%s)? [y/N] ", name, installed)
	if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), message, migrateModulesUninstallYes); confirmError != nil {
		return confirmError
	}
	if removeError := os.Remove(installed); removeError != nil {
		return removeError
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed module %s.\n", name)
	return nil
}

// lookupModuleOnPath finds a module executable on PATH, for the uninstall
// refusal that names where it actually lives.
func lookupModuleOnPath(name string) (string, error) {
	return exec.LookPath(migrate.ExternalModulePrefix + name)
}
