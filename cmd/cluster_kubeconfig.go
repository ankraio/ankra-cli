package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"ankra/internal/kubeconfig"

	"github.com/spf13/cobra"
)

var (
	kubeconfigClusterFlag string
	kubeconfigAllFlag     bool
	kubeconfigEmbedToken  bool
	kubeconfigNamespace   string
	kubeconfigUse         bool
	kubeconfigPrint       bool
	kubeconfigPathFlag    string
	kubeconfigInsecure    bool
	kubeconfigExecCommand string
)

const kubeconfigPageSize = 100

type kubeTarget struct {
	id    string
	name  string
	orgID string
}

var clusterKubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig",
	Short: "Manage Ankra entries in your kubeconfig",
	Long: `Add, remove, and list the Ankra cluster contexts in your kubeconfig.

By default 'add' writes an exec-based context that fetches a short-lived token
on demand via 'ankra cluster kube-token', so credentials stay ephemeral and
SSO-backed (run 'ankra login' once). Other clusters/users/contexts already in
your kubeconfig are left untouched.

These commands read and write a single file: --kubeconfig if given, otherwise
the first entry of $KUBECONFIG, otherwise ~/.kube/config.

Examples:
  ankra cluster kubeconfig add my-cluster --use
  ankra cluster kubeconfig add --all
  ankra cluster kubeconfig add my-cluster --print > my-cluster.yaml
  ankra cluster kubeconfig list
  ankra cluster kubeconfig remove my-cluster
  ankra cluster kubeconfig remove --all`,
}

var clusterKubeconfigAddCmd = &cobra.Command{
	Use:   "add [cluster]",
	Short: "Add or update an Ankra context in your kubeconfig",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := applyKubeconfigPositionalCluster(args); err != nil {
			return err
		}
		return kubeconfigAdd(os.Stdout)
	},
}

var clusterKubeconfigRemoveCmd = &cobra.Command{
	Use:   "remove [cluster]",
	Short: "Remove Ankra contexts from your kubeconfig",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := applyKubeconfigPositionalCluster(args); err != nil {
			return err
		}
		return kubeconfigRemove(os.Stdout)
	},
}

var clusterKubeconfigListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Ankra-managed contexts in your kubeconfig",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return kubeconfigList(os.Stdout)
	},
}

// applyKubeconfigPositionalCluster folds an optional positional cluster
// argument into the --cluster flag so both spellings behave identically
// everywhere downstream.
func applyKubeconfigPositionalCluster(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if kubeconfigClusterFlag != "" && kubeconfigClusterFlag != args[0] {
		return withExitCode(exitUsage, fmt.Errorf("cluster specified twice: %q as argument and %q via --cluster; pass only one", args[0], kubeconfigClusterFlag))
	}
	kubeconfigClusterFlag = args[0]
	return nil
}

func kubeconfigAdd(out io.Writer) error {
	targets, err := resolveKubeconfigTargets()
	if err != nil {
		return err
	}
	emitAddNotes()
	names := resolveContextNames(targets)
	entries, err := buildManagedEntries(targets, names)
	if err != nil {
		return err
	}

	if kubeconfigPrint {
		return printStandaloneKubeconfig(out, entries)
	}

	path, err := resolveKubeconfigPath(kubeconfigPathFlag)
	if err != nil {
		return err
	}
	config, err := kubeconfig.Load(path)
	if err != nil {
		return err
	}

	addedNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		config.Upsert(entry)
		addedNames = append(addedNames, entry.Name)
	}

	if kubeconfigUse {
		if len(addedNames) == 1 {
			config.SetCurrentContext(addedNames[0])
		} else {
			_, _ = fmt.Fprintln(out, "Note: --use ignored because more than one context was added.")
		}
	}

	if err := kubeconfig.Save(path, config); err != nil {
		return err
	}

	for _, name := range addedNames {
		_, _ = fmt.Fprintf(out, "Added context %q to %s\n", name, path)
	}
	if len(addedNames) == 1 {
		suffix := ""
		if kubeconfigUse {
			suffix = " (now active)"
		}
		_, _ = fmt.Fprintf(out, "Use it%s:\n  kubectl --context %s get pods\n", suffix, addedNames[0])
	}
	if kubeconfigEmbedToken {
		_, _ = fmt.Fprintln(out, "Note: embedded tokens are short-lived; re-run to refresh, or drop --embed-token for auto-refreshing exec mode.")
	}
	return nil
}

func kubeconfigRemove(out io.Writer) error {
	path, err := resolveKubeconfigPath(kubeconfigPathFlag)
	if err != nil {
		return err
	}
	config, err := kubeconfig.Load(path)
	if err != nil {
		return err
	}

	removed := make([]string, 0)
	if kubeconfigAllFlag {
		for _, name := range config.ManagedContextNames() {
			if config.Remove(name) {
				removed = append(removed, name)
			}
		}
	} else {
		clusterName, resolveErr := resolveKubeconfigClusterName()
		if resolveErr != nil {
			return resolveErr
		}
		name := kubeconfig.ContextName(clusterName)
		if config.Remove(name) {
			removed = append(removed, name)
		}
	}

	if len(removed) == 0 {
		_, _ = fmt.Fprintf(out, "No matching Ankra contexts found in %s\n", path)
		return nil
	}
	if err := kubeconfig.Save(path, config); err != nil {
		return err
	}
	for _, name := range removed {
		_, _ = fmt.Fprintf(out, "Removed context %q from %s\n", name, path)
	}
	return nil
}

func kubeconfigList(out io.Writer) error {
	path, err := resolveKubeconfigPath(kubeconfigPathFlag)
	if err != nil {
		return err
	}
	config, err := kubeconfig.Load(path)
	if err != nil {
		return err
	}
	names := config.ManagedContextNames()
	if len(names) == 0 {
		_, _ = fmt.Fprintf(out, "No Ankra-managed contexts in %s\n", path)
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "CONTEXT\tSERVER\tACTIVE")
	for _, name := range names {
		active := ""
		if config.CurrentContext == name {
			active = "*"
		}
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\n", name, config.ClusterServer(name), active)
	}
	return writer.Flush()
}

func printStandaloneKubeconfig(out io.Writer, entries []kubeconfig.Entry) error {
	config := &kubeconfig.Config{APIVersion: "v1", Kind: "Config"}
	for _, entry := range entries {
		config.Upsert(entry)
	}
	if len(entries) == 1 {
		config.SetCurrentContext(entries[0].Name)
	}
	data, err := kubeconfig.Marshal(config)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

func emitAddNotes() {
	if kubeconfigAllFlag && kubeconfigClusterFlag != "" {
		fmt.Fprintln(os.Stderr, "Note: the specified cluster is ignored when --all is set.")
	}
	if kubeconfigAllFlag && kubeconfigEmbedToken {
		fmt.Fprintln(os.Stderr, "Note: --embed-token mints one token per cluster and may hit the mint rate limit on large fleets; the default exec mode mints only once.")
	}
}

// resolveContextNames assigns a unique kubeconfig context name to each target.
// The common (no-collision) case yields the backend-consistent "ankra-<slug>";
// names that would collide (e.g. two clusters whose names slugify identically)
// are disambiguated with a short cluster-ID suffix so no entry is silently
// overwritten under --all.
func resolveContextNames(targets []kubeTarget) []string {
	names := make([]string, len(targets))
	used := make(map[string]bool, len(targets))
	for index, target := range targets {
		name := kubeconfig.ContextName(target.name)
		if used[name] {
			suffix := target.id
			if len(suffix) > 8 {
				suffix = suffix[:8]
			}
			name = name + "-" + suffix
		}
		for used[name] {
			name = fmt.Sprintf("%s-%d", name, index)
		}
		used[name] = true
		names[index] = name
	}
	return names
}

// buildManagedEntries constructs the kubeconfig entries for every target. The
// proxy server URL is always sourced from the backend (the canonical
// CLUSTER_ACCESS_PUBLIC_BASE_URL), never guessed from the CLI's own base URL,
// so it points at the public API host regardless of how the CLI is configured.
//
// Embed-token mode mints one token per cluster (it needs the token anyway).
// Exec mode mints a single token to learn the platform-wide proxy base URL and
// to verify access, then reuses that base for every target.
//
// Exec args pin the cluster's owning organisation with --org so kube-token
// keeps working after the user switches their selected organisation; without
// it the token mint 404s ("Cluster not found") whenever the selection differs
// from the cluster's organisation. The organisation ID is used rather than the
// slug so the entry survives organisation renames.
func buildManagedEntries(targets []kubeTarget, names []string) ([]kubeconfig.Entry, error) {
	entries := make([]kubeconfig.Entry, len(targets))

	if kubeconfigEmbedToken {
		for index, target := range targets {
			token, err := apiClient.GetClusterKubeToken(context.Background(), target.id)
			if err != nil {
				return nil, decorateKubeTokenError(fmt.Errorf("mint token for %s: %w", target.name, err), target.name, target.id)
			}
			entry, buildErr := kubeconfig.BuildTokenEntry(names[index], token.Server, token.Token, kubeconfigNamespace, kubeconfigInsecure)
			if buildErr != nil {
				return nil, buildErr
			}
			entries[index] = entry
		}
		return entries, nil
	}

	base, err := resolveProxyBaseURL(targets[0])
	if err != nil {
		return nil, err
	}
	fallbackOrgID := ""
	for index, target := range targets {
		server := base + proxyServerPath(target.id)
		orgID := target.orgID
		if orgID == "" {
			// The backend lookup supplied no owner (a raw cluster-ID
			// passthrough). The token mint only succeeds inside the owning
			// organisation, so whatever this invocation is scoped to is that
			// organisation.
			if fallbackOrgID == "" {
				fallbackOrgID = effectiveOrganisationID()
			}
			orgID = fallbackOrgID
		}
		args := []string{"cluster", "kube-token", "--cluster", target.id}
		if orgID != "" {
			args = append(args, "--org", orgID)
		}
		entry, buildErr := kubeconfig.BuildExecEntry(names[index], server, kubeconfigExecCommand, args, kubeconfigNamespace, kubeconfigInsecure)
		if buildErr != nil {
			return nil, buildErr
		}
		entries[index] = entry
	}
	return entries, nil
}

func proxyServerPath(clusterID string) string {
	return "/api/v1/clusters/" + clusterID + "/k8s"
}

// resolveProxyBaseURL learns the platform-wide kube-proxy base URL from the
// backend by minting one token for a sample cluster and stripping the
// per-cluster path. This also verifies the caller has access before any
// context is written.
func resolveProxyBaseURL(sample kubeTarget) (string, error) {
	token, err := apiClient.GetClusterKubeToken(context.Background(), sample.id)
	if err != nil {
		return "", decorateKubeTokenError(fmt.Errorf("resolve kube proxy URL for %s: %w", sample.name, err), sample.name, sample.id)
	}
	suffix := proxyServerPath(sample.id)
	if !strings.HasSuffix(token.Server, suffix) {
		return "", fmt.Errorf("unexpected kube proxy server URL from backend: %q", token.Server)
	}
	return strings.TrimSuffix(token.Server, suffix), nil
}

func resolveKubeconfigTargets() ([]kubeTarget, error) {
	if kubeconfigAllFlag {
		return listAllClusterTargets()
	}
	if kubeconfigClusterFlag != "" {
		cluster, err := apiClient.GetCluster(kubeconfigClusterFlag)
		if err == nil {
			return []kubeTarget{{id: cluster.ID, name: cluster.Name, orgID: cluster.OrganisationID}}, nil
		}
		if isLikelyClusterID(kubeconfigClusterFlag) {
			// GetCluster is a name lookup, so UUID input lands here. Resolve
			// the ID properly to learn the owning organisation — pinning the
			// locally selected org instead would bake a wrong --org into the
			// kubeconfig whenever the selection has diverged from the owner.
			if byID, idErr := apiClient.GetClusterByID(kubeconfigClusterFlag); idErr == nil {
				return []kubeTarget{{id: byID.ID, name: byID.Name, orgID: byID.OrganisationID}}, nil
			}
			// Not in the organisation in scope. Adding a context for a
			// cluster you were handed the ID of is exactly when the selection
			// is wrong, so find the owner rather than writing a context that
			// pins the wrong organisation and 404s on first use.
			search, adopted := resolveOwningOrganisation(kubeconfigClusterFlag, os.Stderr)
			if adopted {
				return []kubeTarget{{id: search.cluster.ID, name: search.cluster.Name, orgID: search.owner.OrganisationID}}, nil
			}
			if search.err == nil {
				// The search settled the question: the cluster is either
				// somewhere the caller pinned away from, or nowhere. Writing
				// a raw-ID context now would pin the wrong organisation and
				// produce a context that fails on first use.
				return nil, withExitCode(exitNotFound, notInScopedOrganisationError(search, kubeconfigClusterFlag, nil))
			}
			// Inconclusive: leave the ID to the backend, as before.
			return []kubeTarget{{id: kubeconfigClusterFlag, name: kubeconfigClusterFlag}}, nil
		}
		return nil, fmt.Errorf("cluster %q not found; pass a cluster name or ID: %w", kubeconfigClusterFlag, err)
	}
	selected, err := loadSelectedCluster()
	if err != nil || selected.ID == "" {
		return nil, errors.New("no cluster specified; pass a cluster name or ID, use --all, or run 'ankra cluster select' first")
	}
	return []kubeTarget{{id: selected.ID, name: selected.Name, orgID: selected.OrganisationID}}, nil
}

func resolveKubeconfigClusterName() (string, error) {
	if kubeconfigClusterFlag != "" {
		cluster, err := apiClient.GetCluster(kubeconfigClusterFlag)
		if err == nil {
			return cluster.Name, nil
		}
		return kubeconfigClusterFlag, nil
	}
	selected, err := loadSelectedCluster()
	if err != nil || selected.Name == "" {
		return "", errors.New("no cluster specified; pass a cluster name or ID, or use --all")
	}
	return selected.Name, nil
}

func listAllClusterTargets() ([]kubeTarget, error) {
	var targets []kubeTarget
	for page := 1; page <= 1000; page++ {
		response, err := apiClient.ListClusters(page, kubeconfigPageSize)
		if err != nil {
			return nil, err
		}
		for _, cluster := range response.Result {
			targets = append(targets, kubeTarget{id: cluster.ID, name: cluster.Name, orgID: cluster.OrganisationID})
		}
		if len(response.Result) == 0 || response.Pagination.Page >= response.Pagination.TotalPages {
			break
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("no clusters found")
	}
	return targets, nil
}

func resolveKubeconfigPath(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		parts := filepath.SplitList(env)
		if len(parts) > 0 && parts[0] != "" {
			return parts[0], nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}

func init() {
	clusterKubeconfigAddCmd.Flags().StringVar(&kubeconfigClusterFlag, "cluster", "", "Cluster name or ID (same as the positional argument; defaults to the selected cluster)")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigAllFlag, "all", false, "Add every cluster you can access")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigEmbedToken, "embed-token", false, "Embed a short-lived token instead of the exec credential plugin")
	clusterKubeconfigAddCmd.Flags().StringVar(&kubeconfigNamespace, "namespace", "", "Default namespace for the context")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigUse, "use", false, "Set the added context as the active current-context")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigPrint, "print", false, "Print a standalone kubeconfig to stdout instead of writing the file")
	clusterKubeconfigAddCmd.Flags().StringVar(&kubeconfigPathFlag, "kubeconfig", "", "Path to the kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigInsecure, "insecure-skip-tls-verify", false, "Skip TLS verification for the cluster server (dev only)")
	clusterKubeconfigAddCmd.Flags().StringVar(&kubeconfigExecCommand, "exec-command", "ankra", "Executable kubectl invokes for credentials in exec mode (use an absolute path if 'ankra' is not on PATH)")

	clusterKubeconfigRemoveCmd.Flags().StringVar(&kubeconfigClusterFlag, "cluster", "", "Cluster name or ID (same as the positional argument)")
	clusterKubeconfigRemoveCmd.Flags().BoolVar(&kubeconfigAllFlag, "all", false, "Remove all Ankra-managed contexts")
	clusterKubeconfigRemoveCmd.Flags().StringVar(&kubeconfigPathFlag, "kubeconfig", "", "Path to the kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")

	clusterKubeconfigListCmd.Flags().StringVar(&kubeconfigPathFlag, "kubeconfig", "", "Path to the kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")

	clusterKubeconfigCmd.AddCommand(clusterKubeconfigAddCmd)
	clusterKubeconfigCmd.AddCommand(clusterKubeconfigRemoveCmd)
	clusterKubeconfigCmd.AddCommand(clusterKubeconfigListCmd)
	clusterCmd.AddCommand(clusterKubeconfigCmd)
}
