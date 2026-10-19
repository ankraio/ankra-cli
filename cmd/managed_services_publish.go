package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// Authoring a service package: init writes a schema 2 package skeleton
// pinned to one of the organisation's own stack profiles, and publish sends a
// package document to the catalogue. The platform validates every document;
// nothing here decides whether a package is acceptable.

// servicePackageProtocol is what init needs to know about one wire protocol
// a package can declare: the connection fields its consumers receive, in
// delivery order, the URL scheme its clients expect and the capability a
// package speaking it usually provides. It mirrors the platform's protocol
// table (cluster enginekit/serviceprotocol); a protocol the platform does not
// know is refused when the package is published.
type servicePackageProtocol struct {
	fields     []string
	scheme     string
	capability string
}

var servicePackageProtocols = map[string]servicePackageProtocol{
	"postgres":        {fields: []string{"url", "jdbc_url", "host", "port", "database", "username", "password"}, capability: "database"},
	"redis":           {fields: []string{"url", "host", "port", "username", "password"}, capability: "cache"},
	"nats":            {fields: []string{"url", "username", "password"}, scheme: "nats", capability: "queue"},
	"opensearch":      {fields: []string{"url", "username", "password", "ca_certificate"}, scheme: "https", capability: "search"},
	"s3":              {fields: []string{"endpoint", "access_key_id", "secret_access_key", "region", "force_path_style"}, scheme: "http", capability: "object_storage"},
	"victorialogs":    {fields: []string{"url", "username", "password"}, scheme: "http", capability: "logs"},
	"victoriametrics": {fields: []string{"url", "username", "password"}, scheme: "http", capability: "metrics"},
}

// servicePackageProtocolNames is every protocol init knows, sorted.
func servicePackageProtocolNames() []string {
	names := make([]string, 0, len(servicePackageProtocols))
	for name := range servicePackageProtocols {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// servicePackageSkeleton is a schema 2 package for one stack profile version.
// The connection's Service, port and Secret name are left empty for the
// author to fill in - publication refuses the package until they are - and
// each field is taken from a Secret key of the same name, except the host and
// an S3 endpoint, which come from the primary endpoint, and S3's region and
// path style, which are literals most S3-compatible stores accept.
func servicePackageSkeleton(name string, version string, capability string, protocolName string, pin client.ServiceProfilePin) client.ServicePackageContract {
	protocol := servicePackageProtocols[protocolName]
	fields := make(map[string]client.ServicePackageConnectionSource, len(protocol.fields))
	secretKeys := []string{}
	for _, field := range protocol.fields {
		switch field {
		case "host":
			fields[field] = client.ServicePackageConnectionSource{Kind: "primary_host"}
		case "endpoint":
			fields[field] = client.ServicePackageConnectionSource{Kind: "primary_address"}
		case "region":
			fields[field] = client.ServicePackageConnectionSource{Kind: "literal", Value: "us-east-1"}
		case "force_path_style":
			fields[field] = client.ServicePackageConnectionSource{Kind: "literal", Value: "true"}
		default:
			fields[field] = client.ServicePackageConnectionSource{Kind: "secret_key", Value: field}
			secretKeys = append(secretKeys, field)
		}
	}
	return client.ServicePackageContract{
		SchemaVersion: 2,
		Name:          name,
		Version:       version,
		PublisherID:   pin.OrganisationID,
		Capability:    capability,
		Profiles:      map[string]client.ServiceProfileReference{"customer": pin.Profile},
		Parameters:    []client.ServicePackageParameter{},
		Secrets:       []client.ServicePackageSecret{},
		Outputs:       []client.ServicePackageOutput{{Name: "CONNECTION", Kind: "endpoint"}},
		Lifecycle: client.ServicePackageLifecycle{
			Verify: "stack-profile-verify", Upgrade: "stack-profile-upgrade",
			Recovery: "stack-profile-recovery", Delete: "stack-profile-delete",
		},
		Connection: &client.ServicePackageConnection{
			Protocol:   protocolName,
			Endpoints:  []client.ServicePackageConnectionEndpoint{{Name: "primary", Scheme: protocol.scheme}},
			SecretName: "",
			SecretKeys: secretKeys,
			Fields:     fields,
		},
	}
}

func newServicesPackagesInitCommand() *cobra.Command {
	var profileID, profileVersionID, name, version, capability, protocolName, outputPath string
	initCommand := &cobra.Command{
		Use:   "init",
		Short: "Write a package skeleton for one of your stack profiles",
		Long: `Write a service package (contract schema 2) for one version of a stack profile
this organisation owns, so the services it deploys can be set up from the
catalogue and connected to applications like Ankra's own.

The skeleton pins the profile version and names the protocol consumers speak
(` + strings.Join(servicePackageProtocolNames(), ", ") + `). Before publishing, fill in
what only you know about the engine the profile deploys:

  connection.endpoints[0].service  the Service consumers connect to
  connection.endpoints[0].port     its port
  connection.secret_name           the Secret holding its credentials
  connection.fields                which key of that Secret holds each field

Publication refuses the package until these are filled in.`,
		Example: `  ankra services packages init --profile <profile-id> --profile-version <version-id> \
    --name dragonfly-cache --protocol redis -f dragonfly-cache.json
  ankra services packages publish -f dragonfly-cache.json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			protocol, known := servicePackageProtocols[protocolName]
			if !known {
				return fmt.Errorf("unknown protocol %q: choose one of %s", protocolName, strings.Join(servicePackageProtocolNames(), ", "))
			}
			if capability == "" {
				capability = protocol.capability
			}
			pin, pinError := apiClient.GetServiceProfilePin(command.Context(), profileID, profileVersionID)
			if pinError != nil {
				return fmt.Errorf("reading the stack profile version to pin: %w", pinError)
			}
			encoded, encodeError := json.MarshalIndent(servicePackageSkeleton(name, version, capability, protocolName, *pin), "", "  ")
			if encodeError != nil {
				return encodeError
			}
			encoded = append(encoded, '\n')
			if outputPath == "" || outputPath == "-" {
				_, writeError := command.OutOrStdout().Write(encoded)
				return writeError
			}
			if _, statError := os.Stat(outputPath); statError == nil {
				return fmt.Errorf("%s already exists; choose another file or remove it first", outputPath)
			}
			if writeError := os.WriteFile(outputPath, encoded, 0o600); writeError != nil {
				return writeError
			}
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Wrote %s. Fill in the connection's Service, port and Secret, then run:\n  ankra services packages publish -f %s\n", outputPath, outputPath)
			return nil
		},
	}
	initCommand.Flags().StringVar(&profileID, "profile", "", "id of the stack profile the package deploys")
	initCommand.Flags().StringVar(&profileVersionID, "profile-version", "", "id of the profile version to pin")
	initCommand.Flags().StringVar(&name, "name", "", "package name (a DNS label, for example dragonfly-cache)")
	initCommand.Flags().StringVar(&version, "version", "1.0.0", "package version (semantic version)")
	initCommand.Flags().StringVar(&protocolName, "protocol", "", "wire protocol consumers speak: "+strings.Join(servicePackageProtocolNames(), ", "))
	initCommand.Flags().StringVar(&capability, "capability", "", "what the package provides (defaults to the protocol's usual one)")
	initCommand.Flags().StringVarP(&outputPath, "file", "f", "", "write the skeleton to this file instead of standard output")
	for _, required := range []string{"profile", "profile-version", "name", "protocol"} {
		_ = initCommand.MarkFlagRequired(required)
	}
	return initCommand
}

func newServicesPackagesPublishCommand() *cobra.Command {
	var documentPath string
	publishCommand := &cobra.Command{
		Use:   "publish",
		Short: "Publish a package version to this organisation's catalogue",
		Long: `Publish a service package version from its contract document. The document is
sent as written; the platform validates it and refuses it with the path that
failed. A published version is immutable: publishing the same name and version
again with identical content returns it, and different content is refused, so
a change needs a new version.

Publishing makes the package available to set up in this organisation; it does
not make Ankra operate it or qualify it.`,
		Example: "  ankra services packages publish -f dragonfly-cache.json\n  ankra services packages init ... | ankra services packages publish -f -",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			document, readError := readServicePackageDocument(command.InOrStdin(), documentPath)
			if readError != nil {
				return readError
			}
			detail, publishError := apiClient.PublishServicePackage(command.Context(), document)
			if publishError != nil {
				return fmt.Errorf("publishing the package: %w", publishError)
			}
			if rendered, renderError := renderStructured(command, detail); rendered || renderError != nil {
				return renderError
			}
			printServicePackage(command.OutOrStdout(), *detail)
			return nil
		},
	}
	publishCommand.Flags().StringVarP(&documentPath, "file", "f", "", "package document to publish, or - for standard input")
	_ = publishCommand.MarkFlagRequired("file")
	registerStructuredOutputFlags(publishCommand)
	return publishCommand
}

// readServicePackageDocument reads a package document from a file, or from
// standard input for "-", and checks only that it is one JSON object: what it
// says is the platform's to judge.
func readServicePackageDocument(stdin io.Reader, documentPath string) (json.RawMessage, error) {
	var raw []byte
	var readError error
	if documentPath == "-" {
		raw, readError = io.ReadAll(io.LimitReader(stdin, 256*1024+1))
	} else {
		raw, readError = os.ReadFile(documentPath)
	}
	if readError != nil {
		return nil, fmt.Errorf("reading the package document: %w", readError)
	}
	var object map[string]json.RawMessage
	if decodeError := json.Unmarshal(raw, &object); decodeError != nil {
		return nil, errors.New("the package document is not a JSON object")
	}
	return json.RawMessage(raw), nil
}
