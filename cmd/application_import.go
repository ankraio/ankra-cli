package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The limits mirror the platform's, which mirror the design editor's own:
// it drops any file over 2 MiB, and a saved canvas page caps at 16 MiB.
const (
	claudeDesignMaxFileBytes  = 2 << 20
	claudeDesignMaxTotalBytes = 16 << 20
)

// applicationImportOutput is the structured result of an import.
type applicationImportOutput struct {
	ID               string                         `json:"id" yaml:"id"`
	Name             string                         `json:"name" yaml:"name"`
	Repository       string                         `json:"repository" yaml:"repository"`
	RepositoryURL    string                         `json:"repository_url" yaml:"repository_url"`
	Branch           string                         `json:"branch" yaml:"branch"`
	CredentialName   string                         `json:"credential_name" yaml:"credential_name"`
	CommitSHA        string                         `json:"commit_sha,omitempty" yaml:"commit_sha,omitempty"`
	RepositoryReused bool                           `json:"repository_reused" yaml:"repository_reused"`
	Pages            []client.ImportedDesignPage    `json:"pages" yaml:"pages"`
	Warnings         []client.ImportedDesignWarning `json:"warnings" yaml:"warnings"`
	Notes            []string                       `json:"notes,omitempty" yaml:"notes,omitempty"`
}

func newApplicationImportCommand() *cobra.Command {
	importCommand := &cobra.Command{
		Use:   "import",
		Short: "Register an application from something that is not a repository yet",
		Long: `Import an application from a source that has no Git repository of its own.
Ankra converts what you give it, creates a GitHub repository for it, commits
the result and registers the application, so the same setup pull request,
build and deploy lane that follows every registration takes over.`,
	}
	importCommand.AddCommand(newApplicationImportClaudeDesignCommand())
	return importCommand
}

func newApplicationImportClaudeDesignCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "claude-design <path>",
		Short: "Turn a Claude Design export into a deployable application",
		Long: `Turn a Claude Design export into a deployable application.

<path> is what you exported from claude.ai/design: a directory holding the
artboards (<Name>.dc.html), canvas.json and images, a zip of them, a single
artboard, or a saved canvas page (.html). Ankra converts the artboards into a
static site, creates a GitHub repository under the credential's account (or
--owner), commits the site with a Dockerfile, and registers the application.

Artboards that depend on the Claude Design runtime - template holes, sc-for,
sc-if, dc-import or a data-dc-script - are kept as authored and reported as
warnings; they may not render as they did on the canvas.

Running the same import again is safe: the repository keeps a provenance
record, and an unchanged export registers without a second commit.`,
		Example: `  ankra application import claude-design ./neighborly-export --name neighborly
  ankra application import claude-design neighborly.zip --credential github-acme --owner acme
  ankra application import claude-design Neighborly.html --source-url https://claude.ai/design/p/<id> --wait`,
		Args: cobra.ExactArgs(1),
		RunE: runApplicationImportClaudeDesign,
	}
	command.Flags().String("name", "", "Application name (defaults to the export's directory or file name)")
	command.Flags().String("credential", "", "GitHub credential name or ID (auto-detected when omitted)")
	command.Flags().String("owner", "", "GitHub user or organisation to create the repository under (defaults to the credential's account)")
	command.Flags().String("repository", "", "Repository name (defaults to a slug of the application name)")
	command.Flags().String("visibility", "private", "Repository visibility: private or public")
	command.Flags().String("source-url", "", "The claude.ai/design link the export came from, recorded in the repository")
	command.Flags().Bool("wait", false,
		"Wait for Ankra to finish analysing the repository, then print the setup pull request")
	command.Flags().Duration("timeout", applicationAnalysisDefaultTimeout,
		"How long --wait waits before giving up")
	registerStructuredOutputFlags(command)
	return command
}

// claudeDesignExport is what the command read from <path>.
type claudeDesignExport struct {
	files       []client.ImportClaudeDesignFile
	defaultName string
}

// readClaudeDesignExport reads the export at path: every regular file of a
// directory (dotfiles skipped), or the one file named. Names are sent as base
// names; the platform decides what each one is.
func readClaudeDesignExport(path string) (claudeDesignExport, error) {
	information, statError := os.Stat(path)
	if statError != nil {
		return claudeDesignExport{}, withExitCode(exitUsage, fmt.Errorf("reading the export: %w", statError))
	}
	filePaths := []string{}
	defaultName := ""
	if information.IsDir() {
		entries, readError := os.ReadDir(path)
		if readError != nil {
			return claudeDesignExport{}, fmt.Errorf("reading the export directory: %w", readError)
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			filePaths = append(filePaths, filepath.Join(path, entry.Name()))
		}
		if len(filePaths) == 0 {
			return claudeDesignExport{}, withExitCode(exitUsage,
				fmt.Errorf("%s holds no files; export the design from claude.ai/design into it first", path))
		}
		defaultName = filepath.Base(filepath.Clean(path))
	} else {
		filePaths = append(filePaths, path)
		defaultName = filepath.Base(path)
		for _, suffix := range []string{".dc.html", ".html", ".zip"} {
			if strings.HasSuffix(strings.ToLower(defaultName), suffix) {
				defaultName = defaultName[:len(defaultName)-len(suffix)]
				break
			}
		}
	}
	files := make([]client.ImportClaudeDesignFile, 0, len(filePaths))
	total := 0
	for _, filePath := range filePaths {
		content, readError := os.ReadFile(filePath)
		if readError != nil {
			return claudeDesignExport{}, fmt.Errorf("reading %s: %w", filePath, readError)
		}
		isArchive := strings.HasSuffix(strings.ToLower(filePath), ".zip")
		if len(content) > claudeDesignMaxFileBytes && !isArchive {
			return claudeDesignExport{}, withExitCode(exitUsage, fmt.Errorf(
				"%s is %d bytes; the design editor caps a file at %d bytes, so this is not a file it exported",
				filePath, len(content), claudeDesignMaxFileBytes))
		}
		total += len(content)
		if total > claudeDesignMaxTotalBytes {
			return claudeDesignExport{}, withExitCode(exitUsage, fmt.Errorf(
				"the export is over %d bytes; a Claude Design canvas saves under that, so check the directory holds only the export",
				claudeDesignMaxTotalBytes))
		}
		files = append(files, client.ImportClaudeDesignFile{
			Path:          filepath.Base(filePath),
			ContentBase64: base64.StdEncoding.EncodeToString(content),
		})
	}
	return claudeDesignExport{files: files, defaultName: applicationNameSlug(defaultName)}, nil
}

// applicationNameSlug lowercases a name and joins its words with hyphens,
// the shape both an application name and a repository name accept.
func applicationNameSlug(value string) string {
	var builder strings.Builder
	previousHyphen := false
	for _, character := range strings.ToLower(value) {
		isAlphanumeric := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		switch {
		case isAlphanumeric:
			builder.WriteRune(character)
			previousHyphen = false
		case !previousHyphen && builder.Len() > 0:
			builder.WriteByte('-')
			previousHyphen = true
		}
	}
	return strings.TrimSuffix(builder.String(), "-")
}

// resolveImportCredentialAndOwner picks the GitHub credential the way
// `application add` does, then the owner: --owner when given, else the
// credential's own account.
func resolveImportCredentialAndOwner(command *cobra.Command) (client.Credential, string, error) {
	requestedCredential, _ := command.Flags().GetString("credential")
	requestedCredential = strings.TrimSpace(requestedCredential)
	if command.Flags().Changed("credential") && requestedCredential == "" {
		return client.Credential{}, "", withExitCode(exitUsage, errors.New("credential cannot be empty"))
	}
	owner, _ := command.Flags().GetString("owner")
	owner = strings.TrimSpace(owner)
	if command.Flags().Changed("owner") && owner == "" {
		return client.Credential{}, "", withExitCode(exitUsage, errors.New("owner cannot be empty"))
	}
	githubProvider := "github"
	credentials, credentialsError := apiClient.ListCredentials(&githubProvider)
	if credentialsError != nil {
		return client.Credential{}, "", fmt.Errorf("listing GitHub credentials: %w", credentialsError)
	}
	selectedCredential, selectionError := selectApplicationCredential(credentials, owner, requestedCredential)
	if selectionError != nil {
		return client.Credential{}, "", selectionError
	}
	if owner == "" {
		if selectedCredential.AccountLogin == nil || strings.TrimSpace(*selectedCredential.AccountLogin) == "" {
			return client.Credential{}, "", withExitCode(exitUsage, fmt.Errorf(
				"credential %q names no GitHub account; pass --owner <user-or-organisation>", selectedCredential.Name))
		}
		owner = strings.TrimSpace(*selectedCredential.AccountLogin)
	}
	return selectedCredential, owner, nil
}

func runApplicationImportClaudeDesign(command *cobra.Command, arguments []string) error {
	if _, outputError := structuredFormatFromFlags(command); outputError != nil {
		return outputError
	}
	export, exportError := readClaudeDesignExport(arguments[0])
	if exportError != nil {
		return exportError
	}
	applicationName, _ := command.Flags().GetString("name")
	applicationName = strings.TrimSpace(applicationName)
	if applicationName == "" {
		if command.Flags().Changed("name") {
			return withExitCode(exitUsage, errors.New("application name cannot be empty"))
		}
		applicationName = export.defaultName
	}
	if applicationName == "" {
		return withExitCode(exitUsage, errors.New("could not derive an application name from the path; pass --name"))
	}
	visibility, _ := command.Flags().GetString("visibility")
	visibility = strings.ToLower(strings.TrimSpace(visibility))
	if visibility != "private" && visibility != "public" {
		return withExitCode(exitUsage, errors.New("visibility must be private or public"))
	}
	repositoryName, _ := command.Flags().GetString("repository")
	sourceURL, _ := command.Flags().GetString("source-url")

	selectedCredential, owner, resolveError := resolveImportCredentialAndOwner(command)
	if resolveError != nil {
		return resolveError
	}

	result, importError := importClaudeDesign(command.Context(), client.ImportClaudeDesignRequest{
		Name:                     applicationName,
		RepositoryCredentialName: selectedCredential.Name,
		RepositoryOwner:          owner,
		RepositoryName:           strings.TrimSpace(repositoryName),
		Visibility:               visibility,
		SourceURL:                strings.TrimSpace(sourceURL),
		Files:                    export.files,
	})
	if importError != nil {
		return importError
	}
	if rendered, renderError := renderStructured(command, result); rendered || renderError != nil {
		if renderError != nil {
			return renderError
		}
		return waitForApplicationAnalysis(command, result.ID)
	}
	output := command.OutOrStdout()
	_, _ = fmt.Fprintln(output, "Application imported from Claude Design.")
	_, _ = fmt.Fprintf(output, "  ID:         %s\n", result.ID)
	_, _ = fmt.Fprintf(output, "  Name:       %s\n", result.Name)
	_, _ = fmt.Fprintf(output, "  Repository: %s (%s)\n", result.Repository, result.RepositoryURL)
	_, _ = fmt.Fprintf(output, "  Branch:     %s\n", result.Branch)
	_, _ = fmt.Fprintf(output, "  Credential: %s\n", result.CredentialName)
	switch {
	case result.RepositoryReused:
		_, _ = fmt.Fprintln(output, "  Commit:     none - the repository already held this export")
	case result.CommitSHA != "":
		_, _ = fmt.Fprintf(output, "  Commit:     %s\n", result.CommitSHA)
	}
	_, _ = fmt.Fprintf(output, "  Pages:      %d\n", len(result.Pages))
	for _, page := range result.Pages {
		suffix := ""
		if page.Dynamic {
			suffix = "  (uses template logic; served as authored)"
		}
		_, _ = fmt.Fprintf(output, "    %-28s site/%s%s\n", page.Title, page.Path, suffix)
	}
	if len(result.Warnings) > 0 {
		_, _ = fmt.Fprintln(output, "Warnings:")
		for _, warning := range result.Warnings {
			_, _ = fmt.Fprintf(output, "  - %s\n", warning.Message)
		}
	}
	for _, note := range result.Notes {
		_, _ = fmt.Fprintf(output, "  Note: %s\n", note)
	}
	_, _ = fmt.Fprintln(output, "\nAnkra is now analyzing the repository.")
	return waitForApplicationAnalysis(command, result.ID)
}

// importClaudeDesign calls the platform and turns its answer into the
// command's result, or into the platform's refusal.
func importClaudeDesign(requestContext context.Context, importRequest client.ImportClaudeDesignRequest) (applicationImportOutput, error) {
	importResponse, importError := apiClient.ImportClaudeDesignApplication(requestContext, importRequest)
	if importError != nil {
		return applicationImportOutput{}, fmt.Errorf("importing the design: %w", importError)
	}
	if importResponse == nil {
		return applicationImportOutput{}, errors.New("importing the design: platform returned an empty response")
	}
	if len(importResponse.Errors) > 0 {
		return applicationImportOutput{}, applicationCreationError(importResponse.Errors)
	}
	if importResponse.ID == nil || strings.TrimSpace(*importResponse.ID) == "" {
		return applicationImportOutput{}, errors.New("importing the design: platform response did not include an application ID")
	}
	if importResponse.Repository == nil {
		return applicationImportOutput{}, errors.New("importing the design: platform response did not name the repository")
	}
	result := applicationImportOutput{
		ID:               *importResponse.ID,
		Name:             importRequest.Name,
		Repository:       importResponse.Repository.Owner + "/" + importResponse.Repository.Name,
		RepositoryURL:    importResponse.Repository.WebURL,
		Branch:           importResponse.Repository.DefaultBranch,
		CredentialName:   importRequest.RepositoryCredentialName,
		RepositoryReused: importResponse.Repository.Reused,
		Pages:            importResponse.Pages,
		Warnings:         importResponse.Warnings,
		Notes:            importResponse.Notes,
	}
	if importResponse.Repository.CommitSHA != nil {
		result.CommitSHA = *importResponse.Repository.CommitSHA
	}
	if result.Pages == nil {
		result.Pages = []client.ImportedDesignPage{}
	}
	if result.Warnings == nil {
		result.Warnings = []client.ImportedDesignWarning{}
	}
	return result, nil
}
