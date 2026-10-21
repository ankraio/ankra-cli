package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// cloudClusterKindsSentence names, in prose, the cluster kinds Ankra builds
// and can therefore manage nodes, node groups, SSH keys and Kubernetes
// versions for.
const cloudClusterKindsSentence = "Hetzner, OVH, UpCloud, DigitalOcean, Scaleway, Ankra Cloud, AWS, Proxmox VE and HPE Morpheus"

// unsupportedClusterKindError is the refusal for a command run against a
// cluster whose kind cannot do what the command asks (node groups on an
// imported cluster, for example). Naming another cluster is the fix, so it
// is a usage error (exit 2), the same class as `ankra cluster ankracloud
// quota` on a cluster that is not an Ankra Cloud one. A cluster whose kind
// the platform did not report is not known to be the wrong kind, so it keeps
// the generic failure code and says what is missing instead.
func unsupportedClusterKindError(typed, kind, capability string) error {
	if kind == "" {
		return fmt.Errorf("the platform did not report what kind of cluster %s is, so %s cannot be used on it", typed, capability)
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"%s does not support %s (its kind is %s); only %s clusters do",
		typed, capability, kind, cloudClusterKindsSentence))
}

// providerFamily is one `ankra cluster <provider>` command group: the cluster
// kind its commands act on and how prose names that kind.
type providerFamily struct {
	kind    string
	label   string
	article string
}

// providerFamilies are the provider command groups whose commands take a
// cluster as their first argument and call that provider's own routes.
var providerFamilies = map[string]providerFamily{
	"hetzner":      {kind: "hetzner", label: "Hetzner", article: "a"},
	"ovh":          {kind: "ovh", label: "OVH", article: "an"},
	"upcloud":      {kind: "upcloud", label: "UpCloud", article: "an"},
	"digitalocean": {kind: "digitalocean", label: "DigitalOcean", article: "a"},
	"scaleway":     {kind: "scaleway", label: "Scaleway", article: "a"},
	"ankracloud":   {kind: "ankracloud", label: "Ankra Cloud", article: "an"},
	"aws":          {kind: "aws", label: "AWS", article: "an"},
	"proxmox":      {kind: "proxmox", label: "Proxmox VE", article: "a"},
	"morpheus":     {kind: "morpheus", label: "HPE Morpheus", article: "an"},
}

// wrapProviderKindRefusals makes every `ankra cluster <provider> ...`
// command that takes a cluster as its first argument say so when that
// cluster is of another kind. The platform refuses a provider route called
// for a cluster of another provider (409 Conflict), which reached the user
// as "unexpected status: 409 Conflict" and exit 1, as if the platform had
// failed. After such a refusal the cluster's kind is read, and a cluster of
// another kind becomes a usage error (exit 2) naming its kind. Successful
// runs are untouched and cost no extra request. Called from Execute after
// all commands are registered; calling it again changes nothing.
func wrapProviderKindRefusals(root *cobra.Command) {
	clusterCommand, _, findError := root.Find([]string{"cluster"})
	if findError != nil || clusterCommand == nil || clusterCommand == root {
		return
	}
	for _, familyCommand := range clusterCommand.Commands() {
		family, known := providerFamilies[familyCommand.Name()]
		if !known {
			continue
		}
		wrapProviderFamilyCommands(familyCommand, family)
	}
}

// providerKindRefusalAnnotation marks a command whose RunE is already
// wrapped, so wrapping the tree again (tests do) never stacks wrappers.
const providerKindRefusalAnnotation = "ankra/provider-kind-refusal"

func wrapProviderFamilyCommands(command *cobra.Command, family providerFamily) {
	if run := command.RunE; run != nil && takesClusterFirst(command) && command.Annotations[providerKindRefusalAnnotation] == "" {
		if command.Annotations == nil {
			command.Annotations = map[string]string{}
		}
		command.Annotations[providerKindRefusalAnnotation] = family.kind
		command.RunE = func(cmd *cobra.Command, args []string) error {
			runError := run(cmd, args)
			if runError == nil || len(args) == 0 {
				return runError
			}
			return providerKindError(cmd, family, args[0], runError)
		}
	}
	for _, sub := range command.Commands() {
		wrapProviderFamilyCommands(sub, family)
	}
}

// takesClusterFirst reports whether a command's first positional argument
// is a cluster, read from its usage line ("workers <cluster_id|name>").
func takesClusterFirst(command *cobra.Command) bool {
	fields := strings.Fields(command.Use)
	return len(fields) > 1 && strings.HasPrefix(fields[1], "<cluster")
}

// providerKindError turns a platform refusal of a provider command into a
// usage error when the cluster is of another kind. Any other error, an
// error that already carries an exit code, or a cluster whose kind cannot
// be read or matches the family, is returned unchanged.
func providerKindError(cmd *cobra.Command, family providerFamily, typed string, runError error) error {
	var coded *codedError
	if errors.As(runError, &coded) {
		return runError
	}
	var unexpected *client.UnexpectedResponseError
	if !errors.As(runError, &unexpected) {
		return runError
	}
	switch unexpected.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
	default:
		return runError
	}
	clusterID, resolveError := resolveClusterArg(typed)
	if resolveError != nil {
		return runError
	}
	cluster, lookupError := apiClient.GetClusterByID(clusterID)
	if lookupError != nil || cluster.Kind == "" || cluster.Kind == family.kind {
		return runError
	}
	message := fmt.Sprintf("%s is not %s %s cluster (its kind is %s); `%s` applies to %s clusters only",
		typed, family.article, family.label, cluster.Kind, cmd.CommandPath(), family.label)
	if alternative := sameCommandForKind(cmd, family, cluster.Kind); alternative != "" {
		message += fmt.Sprintf(". For this cluster use `%s`", alternative)
	}
	return withExitCode(exitUsage, errors.New(message))
}

// sameCommandForKind names the equivalent command in the cluster's own
// provider group (`ankra cluster ovh workers` for `ankra cluster hetzner
// workers` on an OVH cluster), or "" when that group has no such command.
func sameCommandForKind(cmd *cobra.Command, family providerFamily, kind string) string {
	if _, known := providerFamilies[kind]; !known {
		return ""
	}
	path := strings.Fields(cmd.CommandPath())
	if len(path) < 4 || path[2] != family.kind {
		return ""
	}
	path[2] = kind
	found, _, findError := cmd.Root().Find(path[1:])
	if findError != nil || found == nil || found.CommandPath() != strings.Join(path, " ") || found.Deprecated != "" {
		return ""
	}
	return found.CommandPath()
}
