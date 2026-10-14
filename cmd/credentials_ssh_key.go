package cmd

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// The org-wide SSH-key credential commands.
//
// An SSH-key credential belongs to the organisation, not to a provider: one
// credential can be attached to Hetzner, OVH, UpCloud, Ankra Cloud, ... clusters
// alike (`ankra cluster ssh-keys set`). The per-provider `credentials <provider>
// ssh-key` groups reach the same credential through a provider-named route;
// this group says what the credential actually is.

var credentialsSSHKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage the organisation's SSH key credentials",
	Long: `Create and list SSH key credentials.

An SSH key credential holds one public key and belongs to the whole
organisation: attach it to any cloud cluster with
'ankra cluster ssh-keys set <cluster> --ssh-key-credential-ids <id>'.
'ankra credentials get <name>' shows the public key a credential holds and
its SHA256 fingerprint.`,
}

var credentialsSSHKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's SSH key credentials",
	Long: `List the organisation's SSH key credentials.

The platform's list does not include the public key or its fingerprint
today; a Fingerprint column appears when it does. To see which key a
credential holds, run 'ankra credentials get <name>'.`,
	Example: `  ankra credentials ssh-key list
  ankra credentials ssh-key list -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, err := apiClient.ListOrgSSHKeyCredentials()
		if err != nil {
			return fmt.Errorf("listing SSH key credentials: %w", err)
		}
		if credentials == nil {
			credentials = []client.SSHKeyCredentialListItem{}
		}
		if rendered, err := renderStructured(cmd, credentials); rendered || err != nil {
			return err
		}

		if len(credentials) == 0 {
			fmt.Println("No SSH key credentials found. Create one with 'ankra credentials ssh-key create'.")
			return nil
		}

		showFingerprint := false
		for _, credential := range credentials {
			if credential.Fingerprint != nil && *credential.Fingerprint != "" {
				showFingerprint = true
				break
			}
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		header := table.Row{"ID", "Name", "Available", "Created"}
		if showFingerprint {
			header = table.Row{"ID", "Name", "Fingerprint", "Available", "Created"}
		}
		t.AppendHeader(header)
		for _, credential := range credentials {
			available := "yes"
			if !credential.Available {
				available = "no"
			}
			row := table.Row{credential.ID, credential.Name, available, formatTimeAgo(credential.CreatedAt)}
			if showFingerprint {
				fingerprint := "-"
				if credential.Fingerprint != nil && *credential.Fingerprint != "" {
					fingerprint = *credential.Fingerprint
				}
				row = table.Row{credential.ID, credential.Name, fingerprint, available, formatTimeAgo(credential.CreatedAt)}
			}
			t.AppendRow(row)
		}
		t.Render()
		return nil
	},
}

// sshKeyCreateResult is the structured (-o json|yaml) answer of
// `credentials ssh-key create`. The generated private key is never part of
// it: it goes to --private-key-file only, so it cannot end up in a pipe or
// a CI log by way of -o json.
type sshKeyCreateResult struct {
	ID             string `json:"id" yaml:"id"`
	Name           string `json:"name" yaml:"name"`
	Fingerprint    string `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`
	Generated      bool   `json:"generated" yaml:"generated"`
	PrivateKeyFile string `json:"private_key_file,omitempty" yaml:"private_key_file,omitempty"`
}

var credentialsSSHKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SSH key credential from a public key, or generate one",
	Long: `Create an organisation-wide SSH key credential.

Give the public key inline (--public-key), from a file (--public-key-file,
for example ~/.ssh/id_ed25519.pub), or let the platform generate a new
keypair (--generate). A generated private key is shown once and never stored
by Ankra, so --generate requires --private-key-file: the key is written there
with mode 0600, and the command refuses to overwrite an existing file.

A private key passed by mistake (for example ~/.ssh/id_ed25519 instead of the
.pub file) is refused before anything is sent.

The platform refuses a public key the organisation already holds and names
the credential that holds it.`,
	Example: `  ankra credentials ssh-key create --name ops-laptop --public-key-file ~/.ssh/id_ed25519.pub
  ankra credentials ssh-key create --name ops-laptop --public-key "ssh-ed25519 AAAA... me@laptop"
  ankra credentials ssh-key create --name break-glass --generate --private-key-file ./break-glass.pem`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		publicKey, _ := cmd.Flags().GetString("public-key")
		publicKeyFile, _ := cmd.Flags().GetString("public-key-file")
		generate, _ := cmd.Flags().GetBool("generate")
		privateKeyFile, _ := cmd.Flags().GetString("private-key-file")

		name = strings.TrimSpace(name)
		if name == "" {
			return withExitCode(exitUsage, errors.New("--name is required"))
		}

		sources := 0
		for _, given := range []bool{cmd.Flags().Changed("public-key"), cmd.Flags().Changed("public-key-file"), generate} {
			if given {
				sources++
			}
		}
		if sources != 1 {
			return withExitCode(exitUsage, errors.New("give exactly one of --public-key, --public-key-file or --generate"))
		}
		if generate && strings.TrimSpace(privateKeyFile) == "" {
			return withExitCode(exitUsage, errors.New(
				"--generate requires --private-key-file <path>: the generated private key is shown once and Ankra does not keep it"))
		}
		if !generate && cmd.Flags().Changed("private-key-file") {
			return withExitCode(exitUsage, errors.New("--private-key-file is only used with --generate"))
		}

		request := client.CreateSSHKeyCredentialRequest{Name: name, Generate: generate}
		fingerprint := ""
		if !generate {
			if cmd.Flags().Changed("public-key-file") {
				contents, readError := os.ReadFile(publicKeyFile)
				if readError != nil {
					return withExitCode(exitUsage, fmt.Errorf("reading --public-key-file: %w", readError))
				}
				publicKey = string(contents)
			}
			normalised, validateError := normalisePublicKeyInput(publicKey)
			if validateError != nil {
				return withExitCode(exitUsage, validateError)
			}
			request.SSHPublicKey = &normalised
			if parsed, ok := parseSSHPublicKey(normalised); ok {
				fingerprint = parsed.Fingerprint
			}
		}

		// Claim the private key file before asking the platform for a
		// keypair: a path that cannot be written must fail now, not after
		// the platform has generated a key whose private half would then
		// exist nowhere.
		var privateKeyOutput *os.File
		if generate {
			file, openError := os.OpenFile(privateKeyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if openError != nil {
				if errors.Is(openError, os.ErrExist) {
					return withExitCode(exitUsage, fmt.Errorf("--private-key-file %s already exists; choose a new path", privateKeyFile))
				}
				return withExitCode(exitUsage, fmt.Errorf("cannot create --private-key-file: %w", openError))
			}
			privateKeyOutput = file
		}
		abandonPrivateKeyFile := func() {
			if privateKeyOutput != nil {
				_ = privateKeyOutput.Close()
				_ = os.Remove(privateKeyFile)
			}
		}

		created, createError := apiClient.CreateOrgSSHKeyCredential(request)
		if createError != nil {
			abandonPrivateKeyFile()
			return fmt.Errorf("creating SSH key credential %q: %w", name, createError)
		}

		result := sshKeyCreateResult{ID: created.ID, Name: created.Name, Fingerprint: fingerprint, Generated: generate}
		if result.Name == "" {
			result.Name = name
		}
		if generate {
			if created.PrivateKey == nil || *created.PrivateKey == "" {
				abandonPrivateKeyFile()
				return fmt.Errorf("SSH key credential %q was created (id %s) but the platform returned no private key; delete it with 'ankra credentials delete %s' and try again",
					result.Name, result.ID, result.ID)
			}
			if writeError := writePrivateKey(privateKeyOutput, *created.PrivateKey); writeError != nil {
				_ = os.Remove(privateKeyFile)
				// The platform will not show this key again. Losing it to a
				// failed write would leave a credential nobody can log in
				// with, so it goes to stderr as the last resort.
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"Warning: writing %s failed (%v). The generated private key follows; save it now, it will not be shown again:\n%s\n",
					privateKeyFile, writeError, *created.PrivateKey)
				return fmt.Errorf("SSH key credential %q was created (id %s) but its private key could not be written: %w", result.Name, result.ID, writeError)
			}
			result.PrivateKeyFile = privateKeyFile
		}

		if rendered, err := renderStructured(cmd, result); rendered || err != nil {
			return err
		}

		fmt.Printf("SSH key credential '%s' created.\n", result.Name)
		fmt.Printf("  ID:          %s\n", result.ID)
		if result.Fingerprint != "" {
			fmt.Printf("  Fingerprint: %s\n", result.Fingerprint)
		}
		if result.PrivateKeyFile != "" {
			fmt.Printf("  Private key: written to %s (mode 0600). Ankra does not keep a copy.\n", result.PrivateKeyFile)
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"\nAttach it to a cluster with 'ankra cluster ssh-keys set <cluster> --ssh-key-credential-ids <ids>' (the list replaces the attached set; see 'ankra cluster ssh-keys get <cluster>').\n")
		return nil
	},
}

func writePrivateKey(file *os.File, privateKey string) error {
	contents := privateKey
	if !strings.HasSuffix(contents, "\n") {
		contents += "\n"
	}
	if _, err := io.WriteString(file, contents); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// normalisePublicKeyInput checks what the user gave as a public key before
// it leaves the machine. It refuses private key material outright (the
// usual slip is ~/.ssh/id_ed25519 for ~/.ssh/id_ed25519.pub) and more than
// one key, since a credential holds exactly one. Anything else is passed
// through trimmed: the platform is the authority on what a valid key is.
func normalisePublicKeyInput(raw string) (string, error) {
	if strings.Contains(raw, "PRIVATE KEY") {
		return "", errors.New("that is a private key, not a public key; nothing was sent. Pass the .pub file (for example ~/.ssh/id_ed25519.pub)")
	}
	var keyLines []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		keyLines = append(keyLines, trimmed)
	}
	switch len(keyLines) {
	case 0:
		return "", errors.New("the public key is empty")
	case 1:
		return keyLines[0], nil
	default:
		return "", fmt.Errorf("found %d keys; an SSH key credential holds one, so create one credential per key", len(keyLines))
	}
}

// parsedSSHPublicKey is an OpenSSH public key line taken apart.
type parsedSSHPublicKey struct {
	KeyType     string
	Fingerprint string
	Comment     string
}

// parseSSHPublicKey reads an OpenSSH public key line ("<type> <base64>
// [comment]", optionally preceded by authorized_keys options) and computes
// its SHA256 fingerprint the way `ssh-keygen -l` does: the unpadded base64
// of the SHA-256 of the decoded key blob. It reports false for anything it
// cannot take apart, rather than guessing.
func parseSSHPublicKey(line string) (parsedSSHPublicKey, bool) {
	fields := strings.Fields(line)
	for index := 0; index+1 < len(fields); index++ {
		keyType := fields[index]
		if !looksLikeSSHKeyType(keyType) {
			continue
		}
		blob, decodeError := base64.StdEncoding.DecodeString(fields[index+1])
		if decodeError != nil || len(blob) < 4 {
			return parsedSSHPublicKey{}, false
		}
		// The blob starts with its own key type as an SSH string; a line
		// whose two disagree is not a key this can vouch for.
		typeLength := binary.BigEndian.Uint32(blob[:4])
		if uint64(typeLength) > uint64(len(blob)-4) || string(blob[4:4+typeLength]) != keyType {
			return parsedSSHPublicKey{}, false
		}
		digest := sha256.Sum256(blob)
		return parsedSSHPublicKey{
			KeyType:     keyType,
			Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:]),
			Comment:     strings.Join(fields[index+2:], " "),
		}, true
	}
	return parsedSSHPublicKey{}, false
}

func looksLikeSSHKeyType(field string) bool {
	return strings.HasPrefix(field, "ssh-") ||
		strings.HasPrefix(field, "ecdsa-sha2-") ||
		strings.HasPrefix(field, "sk-")
}

func init() {
	credentialsSSHKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	credentialsSSHKeyCreateCmd.Flags().String("public-key", "", "The public key itself, e.g. \"ssh-ed25519 AAAA... me@laptop\"")
	credentialsSSHKeyCreateCmd.Flags().String("public-key-file", "", "Read the public key from this file, e.g. ~/.ssh/id_ed25519.pub")
	credentialsSSHKeyCreateCmd.Flags().Bool("generate", false, "Have the platform generate a new keypair (requires --private-key-file)")
	credentialsSSHKeyCreateCmd.Flags().String("private-key-file", "", "With --generate: write the generated private key here (mode 0600; must not exist)")

	registerStructuredOutputFlags(credentialsSSHKeyListCmd, credentialsSSHKeyCreateCmd)

	credentialsSSHKeyCmd.AddCommand(credentialsSSHKeyListCmd)
	credentialsSSHKeyCmd.AddCommand(credentialsSSHKeyCreateCmd)
	credentialsCmd.AddCommand(credentialsSSHKeyCmd)
}
