package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

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

// addonProfileDocument is the stack profile a single Helm chart becomes when a
// package is made from it: one stack named like the package, holding the
// chart as its only add-on. The add-on's namespace is the stack's name, as in
// Ankra's own service profiles, so every instance of the service installs
// into the namespace named after it and the connection's Service and Secret
// are found there.
func addonProfileDocument(name string, chart string, chartVersion string, registry client.HelmRegistryDetail, values []byte) ([]byte, error) {
	addon := map[string]any{
		"name":          chart,
		"chart_name":    chart,
		"chart_version": chartVersion,
		"registry_name": registry.Name,
		"registry_url":  registry.URL,
		"namespace":     "${{ ankra.stack_name }}",
	}
	if len(values) > 0 {
		addon["configuration"] = map[string]any{"values_base64": base64.StdEncoding.EncodeToString(values)}
	}
	return yaml.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ImportCluster",
		"metadata": map[string]any{
			"name":        name,
			"description": fmt.Sprintf("The %s chart from %s, made into a managed service by ankra services packages init --addon.", chart, registry.Name),
		},
		"spec": map[string]any{"stacks": []any{map[string]any{"name": name, "addons": []any{addon}}}},
	})
}

// profileFromAddon creates the stack profile a chart becomes (addonProfileDocument)
// and answers the profile and the version the import published.
func profileFromAddon(name string, chart string, chartVersion string, registryName string, valuesPath string) (string, string, error) {
	registry, registryError := apiClient.GetHelmRegistry(registryName, 0, 1)
	if registryError != nil {
		return "", "", fmt.Errorf("reading the Helm registry %q: %w", registryName, registryError)
	}
	var values []byte
	if valuesPath != "" {
		read, readError := os.ReadFile(valuesPath)
		if readError != nil {
			return "", "", fmt.Errorf("reading the chart values: %w", readError)
		}
		values = read
	}
	document, documentError := addonProfileDocument(name, chart, chartVersion, registry.Registry, values)
	if documentError != nil {
		return "", "", documentError
	}
	changelog := "Created from the " + chart + " chart " + chartVersion + " to publish it as a managed service."
	imported, importError := apiClient.ImportStackProfile(client.ImportStackProfileRequest{
		Name: &name, Category: "managed-service", Tags: []string{"managed-service", chart},
		ContentBase64: base64.StdEncoding.EncodeToString(document), Changelog: &changelog,
	})
	if importError != nil {
		return "", "", fmt.Errorf("creating a stack profile from the %s chart: %w", chart, importError)
	}
	if imported.Version == nil || imported.Profile.ID == "" || imported.Version.ID == "" {
		return "", "", errors.New("the platform created the stack profile but did not say which version it published; run init again with --profile and --profile-version")
	}
	return imported.Profile.ID, imported.Version.ID, nil
}

func newServicesPackagesInitCommand() *cobra.Command {
	var profileID, profileVersionID, name, version, capability, protocolName, outputPath string
	var addonChart, addonRegistry, addonChartVersion, addonValuesPath string
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

Publication refuses the package until these are filled in.

To make a service from one Helm chart rather than a stack profile, pass
--addon with --registry and --chart-version instead of --profile and
--profile-version: init first creates a stack profile holding that chart
alone (installed into a namespace named after each service), then writes the
package pinned to it.`,
		Example: `  ankra services packages init --profile <profile-id> --profile-version <version-id> \
    --name dragonfly-cache --protocol redis -f dragonfly-cache.json
  ankra services packages init --addon dragonfly --registry dragonfly --chart-version 1.2.0 \
    --values dragonfly-values.yaml --name dragonfly-cache --protocol redis -f dragonfly-cache.json
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
			fromAddon := addonChart != ""
			switch {
			case fromAddon && (profileID != "" || profileVersionID != ""):
				return errors.New("choose either --addon or --profile and --profile-version, not both")
			case fromAddon && (addonRegistry == "" || addonChartVersion == ""):
				return errors.New("--addon needs --registry and --chart-version")
			case !fromAddon && (profileID == "" || profileVersionID == ""):
				return errors.New("pass --profile and --profile-version, or --addon with --registry and --chart-version")
			case !fromAddon && (addonRegistry != "" || addonChartVersion != "" || addonValuesPath != ""):
				return errors.New("--registry, --chart-version and --values go with --addon")
			}
			if fromAddon {
				// Creating the profile is not undone, so a file init could not
				// write is refused before anything is created.
				if outputPath != "" && outputPath != "-" {
					_, statError := os.Stat(outputPath)
					switch {
					case statError == nil:
						return fmt.Errorf("%s already exists; choose another file or remove it first", outputPath)
					case !errors.Is(statError, fs.ErrNotExist):
						// A path that cannot be checked is not known to be writable.
						return fmt.Errorf("checking %s before creating anything: %w", outputPath, statError)
					}
				}
				createdProfile, createdVersion, createError := profileFromAddon(name, addonChart, addonChartVersion, addonRegistry, addonValuesPath)
				if createError != nil {
					return createError
				}
				profileID, profileVersionID = createdProfile, createdVersion
				_, _ = fmt.Fprintf(command.ErrOrStderr(), "Created stack profile %s (%s) from the %s chart %s.\n", name, profileID, addonChart, addonChartVersion)
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
			if writeError := writeNewFile(outputPath, encoded); writeError != nil {
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
	initCommand.Flags().StringVar(&addonChart, "addon", "", "make the service from this Helm chart instead of a stack profile")
	initCommand.Flags().StringVar(&addonRegistry, "registry", "", "the Helm registry --addon comes from (as named in Ankra)")
	initCommand.Flags().StringVar(&addonChartVersion, "chart-version", "", "the chart version --addon pins")
	initCommand.Flags().StringVar(&addonValuesPath, "values", "", "a values file for the --addon chart")
	for _, required := range []string{"name", "protocol"} {
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

// maxServicePackageDocumentBytes is the largest package document the
// platform accepts.
const maxServicePackageDocumentBytes = 256 * 1024

// writeNewFile writes a file that must not exist yet. The create is
// exclusive, so an existing file - or one that appears between any check and
// the write - is never overwritten.
func writeNewFile(path string, content []byte) error {
	file, openError := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(openError, os.ErrExist) {
		return fmt.Errorf("%s already exists; choose another file or remove it first", path)
	}
	if openError != nil {
		return openError
	}
	if _, writeError := file.Write(content); writeError != nil {
		_ = file.Close()
		return writeError
	}
	return file.Close()
}

// readServicePackageDocument reads a package document from a file, or from
// standard input for "-", and checks only that it is one JSON object no
// larger than the platform accepts: what it says is the platform's to judge.
func readServicePackageDocument(stdin io.Reader, documentPath string) (json.RawMessage, error) {
	source := stdin
	if documentPath != "-" {
		file, openError := os.Open(documentPath)
		if openError != nil {
			return nil, fmt.Errorf("reading the package document: %w", openError)
		}
		defer func() { _ = file.Close() }()
		source = file
	}
	raw, readError := io.ReadAll(io.LimitReader(source, maxServicePackageDocumentBytes+1))
	if readError != nil {
		return nil, fmt.Errorf("reading the package document: %w", readError)
	}
	if len(raw) > maxServicePackageDocumentBytes {
		return nil, fmt.Errorf("the package document is larger than the %d KiB the platform accepts", maxServicePackageDocumentBytes/1024)
	}
	var object map[string]json.RawMessage
	if decodeError := json.Unmarshal(raw, &object); decodeError != nil || object == nil {
		return nil, errors.New("the package document is not a JSON object")
	}
	return json.RawMessage(raw), nil
}
