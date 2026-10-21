package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
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

// parseGeneratedKeys reads --generate-key values: "key" for a random value of
// the platform's default length, "key:length" for a random value of that
// many characters, and "key=value" for a fixed value (a user or database name
// the chart is told to create). The platform validates names and bounds.
func parseGeneratedKeys(specifications []string) ([]client.GeneratedServiceSecretKey, error) {
	keys := make([]client.GeneratedServiceSecretKey, 0, len(specifications))
	for _, specification := range specifications {
		if key, value, isFixed := strings.Cut(specification, "="); isFixed {
			if key == "" || value == "" {
				return nil, fmt.Errorf("--generate-key %q: write key=value for a fixed value", specification)
			}
			keys = append(keys, client.GeneratedServiceSecretKey{Key: key, Value: value})
			continue
		}
		key, lengthText, hasLength := strings.Cut(specification, ":")
		if key == "" {
			return nil, fmt.Errorf("--generate-key %q: name the key", specification)
		}
		generated := client.GeneratedServiceSecretKey{Key: key}
		if hasLength {
			length, parseError := strconv.Atoi(lengthText)
			if parseError != nil || length <= 0 {
				return nil, fmt.Errorf("--generate-key %q: the length after : must be a whole number of characters", specification)
			}
			generated.Length = length
		}
		keys = append(keys, generated)
	}
	return keys, nil
}

// profileFromAddon asks the platform to make one Helm chart into a stack
// profile (POST /api/v1/org/stack-profiles/from-chart) and answers the
// profile and the version it published. The platform resolves the registry
// and builds the profile document, with a credentials release first when
// generated names a Secret.
func profileFromAddon(name string, chart string, chartVersion string, registryName string, valuesPath string,
	generated *client.GeneratedServiceSecret) (string, string, error) {
	chartRequest := client.ChartServiceProfileRequest{
		Name: name, ChartName: chart, ChartVersion: chartVersion, RegistryName: registryName, GeneratedSecret: generated,
	}
	if valuesPath != "" {
		values, readError := os.ReadFile(valuesPath)
		if readError != nil {
			return "", "", fmt.Errorf("reading the chart values: %w", readError)
		}
		chartRequest.ValuesYAML = string(values)
	}
	created, createError := apiClient.CreateChartServiceProfile(chartRequest)
	if createError != nil {
		return "", "", fmt.Errorf("creating a stack profile from the %s chart: %w", chart, createError)
	}
	if created.Version == nil || created.Profile.ID == "" || created.Version.ID == "" {
		return "", "", errors.New("the platform created the stack profile but did not say which version it published; run init again with --profile and --profile-version")
	}
	return created.Profile.ID, created.Version.ID, nil
}

func newServicesPackagesInitCommand() *cobra.Command {
	var profileID, profileVersionID, name, version, capability, protocolName, outputPath string
	var addonChart, addonRegistry, addonChartVersion, addonValuesPath, generatedSecretName string
	var generatedKeySpecifications []string
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
package pinned to it.

With --generate-secret, that profile also generates the service's
credentials: a Secret of that name in each service's namespace, created on
the first install and kept unchanged across upgrades, whose values Ankra
never sees. Name its keys with --generate-key: "password" for a random
value, "password:48" for a random value of 48 characters, "username=app" for
a fixed value. Point the chart at the Secret in --values (most charts take
an existing-secret setting); the package's connection names it already.`,
		Example: `  ankra services packages init --profile <profile-id> --profile-version <version-id> \
    --name dragonfly-cache --protocol redis -f dragonfly-cache.json
  ankra services packages init --addon dragonfly --registry dragonfly --chart-version 1.2.0 \
    --values dragonfly-values.yaml --name dragonfly-cache --protocol redis -f dragonfly-cache.json
  ankra services packages init --addon postgresql --registry bitnami --chart-version 16.4.2 \
    --generate-secret orders-db-auth --generate-key username=app --generate-key password \
    --values orders-db-values.yaml --name orders-db --protocol postgres -f orders-db.json
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
			case !fromAddon && (generatedSecretName != "" || len(generatedKeySpecifications) > 0):
				return errors.New("--generate-secret and --generate-key go with --addon")
			case generatedSecretName == "" && len(generatedKeySpecifications) > 0:
				return errors.New("--generate-key needs --generate-secret, the Secret its keys go into")
			case generatedSecretName != "" && len(generatedKeySpecifications) == 0:
				return errors.New("--generate-secret needs at least one --generate-key")
			case !fromAddon && (addonRegistry != "" || addonChartVersion != "" || addonValuesPath != ""):
				return errors.New("--registry, --chart-version and --values go with --addon")
			}
			// reserved is the output file, created empty before the profile
			// when --addon will create one: creating a profile is not undone,
			// so a file init could not write is refused before anything
			// exists, and removed again if a later step fails.
			var reserved *os.File
			if fromAddon && outputPath != "" && outputPath != "-" {
				file, openError := openNewFile(outputPath)
				if openError != nil {
					return fmt.Errorf("preparing %s before creating anything: %w", outputPath, openError)
				}
				reserved = file
				defer func() {
					if reserved != nil {
						_ = reserved.Close()
						_ = os.Remove(outputPath)
					}
				}()
			}
			if fromAddon {
				var generated *client.GeneratedServiceSecret
				if generatedSecretName != "" {
					keys, keysError := parseGeneratedKeys(generatedKeySpecifications)
					if keysError != nil {
						return keysError
					}
					generated = &client.GeneratedServiceSecret{Name: generatedSecretName, Keys: keys}
				}
				createdProfile, createdVersion, createError := profileFromAddon(name, addonChart, addonChartVersion, addonRegistry, addonValuesPath, generated)
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
			skeleton := servicePackageSkeleton(name, version, capability, protocolName, *pin)
			if generatedSecretName != "" {
				skeleton.Connection.SecretName = generatedSecretName
			}
			encoded, encodeError := json.MarshalIndent(skeleton, "", "  ")
			if encodeError != nil {
				return encodeError
			}
			encoded = append(encoded, '\n')
			if outputPath == "" || outputPath == "-" {
				_, writeError := command.OutOrStdout().Write(encoded)
				return writeError
			}
			if reserved != nil {
				file := reserved
				reserved = nil
				if _, writeError := file.Write(encoded); writeError != nil {
					_ = file.Close()
					return writeError
				}
				if closeError := file.Close(); closeError != nil {
					return closeError
				}
			} else if writeError := writeNewFile(outputPath, encoded); writeError != nil {
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
	initCommand.Flags().StringVar(&generatedSecretName, "generate-secret", "", "with --addon, a Secret of this name the profile generates in each service's namespace")
	initCommand.Flags().StringArrayVar(&generatedKeySpecifications, "generate-key", nil, "a key of --generate-secret: key (random), key:length (random of that length) or key=value (fixed); repeatable")
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
	file, openError := openNewFile(path)
	if openError != nil {
		return openError
	}
	if _, writeError := file.Write(content); writeError != nil {
		_ = file.Close()
		return writeError
	}
	return file.Close()
}

// openNewFile creates path for writing, refusing a file that already exists
// rather than overwriting it.
func openNewFile(path string) (*os.File, error) {
	file, openError := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(openError, os.ErrExist) {
		return nil, fmt.Errorf("%s already exists; choose another file or remove it first", path)
	}
	return file, openError
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
