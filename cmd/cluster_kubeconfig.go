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
	id   string
	name string
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
  ankra cluster kubeconfig add --cluster my-cluster --use
  ankra cluster kubeconfig add --all
  ankra cluster kubeconfig add --cluster my-cluster --print > my-cluster.yaml
  ankra cluster kubeconfig list
  ankra cluster kubeconfig remove --cluster my-cluster
  ankra cluster kubeconfig remove --all`,
}

var clusterKubeconfigAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add or update an Ankra context in your kubeconfig",
	Run: func(cmd *cobra.Command, args []string) {
		if err := kubeconfigAdd(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var clusterKubeconfigRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove Ankra contexts from your kubeconfig",
	Run: func(cmd *cobra.Command, args []string) {
		if err := kubeconfigRemove(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var clusterKubeconfigListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Ankra-managed contexts in your kubeconfig",
	Run: func(cmd *cobra.Command, args []string) {
		if err := kubeconfigList(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
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
		fmt.Fprintln(os.Stderr, "Note: --cluster is ignored when --all is set.")
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
func buildManagedEntries(targets []kubeTarget, names []string) ([]kubeconfig.Entry, error) {
	entries := make([]kubeconfig.Entry, len(targets))

	if kubeconfigEmbedToken {
		for index, target := range targets {
			token, err := apiClient.GetClusterKubeToken(context.Background(), target.id)
			if err != nil {
				return nil, fmt.Errorf("mint token for %s: %w", target.name, err)
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
	for index, target := range targets {
		server := base + proxyServerPath(target.id)
		args := []string{"cluster", "kube-token", "--cluster", target.id}
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
		return "", fmt.Errorf("resolve kube proxy URL for %s: %w", sample.name, err)
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
			return []kubeTarget{{id: cluster.ID, name: cluster.Name}}, nil
		}
		if isLikelyClusterID(kubeconfigClusterFlag) {
			return []kubeTarget{{id: kubeconfigClusterFlag, name: kubeconfigClusterFlag}}, nil
		}
		return nil, fmt.Errorf("cluster %q not found; pass a cluster name or ID", kubeconfigClusterFlag)
	}
	selected, err := loadSelectedCluster()
	if err != nil || selected.ID == "" {
		return nil, errors.New("no cluster specified; pass --cluster <name|id>, use --all, or run 'ankra cluster select' first")
	}
	return []kubeTarget{{id: selected.ID, name: selected.Name}}, nil
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
		return "", errors.New("no cluster specified; pass --cluster <name|id> or --all")
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
			targets = append(targets, kubeTarget{id: cluster.ID, name: cluster.Name})
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
	clusterKubeconfigAddCmd.Flags().StringVar(&kubeconfigClusterFlag, "cluster", "", "Cluster name or ID (defaults to the selected cluster)")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigAllFlag, "all", false, "Add every cluster you can access")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigEmbedToken, "embed-token", false, "Embed a short-lived token instead of the exec credential plugin")
	clusterKubeconfigAddCmd.Flags().StringVar(&kubeconfigNamespace, "namespace", "", "Default namespace for the context")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigUse, "use", false, "Set the added context as the active current-context")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigPrint, "print", false, "Print a standalone kubeconfig to stdout instead of writing the file")
	clusterKubeconfigAddCmd.Flags().StringVar(&kubeconfigPathFlag, "kubeconfig", "", "Path to the kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	clusterKubeconfigAddCmd.Flags().BoolVar(&kubeconfigInsecure, "insecure-skip-tls-verify", false, "Skip TLS verification for the cluster server (dev only)")
	clusterKubeconfigAddCmd.Flags().StringVar(&kubeconfigExecCommand, "exec-command", "ankra", "Executable kubectl invokes for credentials in exec mode (use an absolute path if 'ankra' is not on PATH)")

	clusterKubeconfigRemoveCmd.Flags().StringVar(&kubeconfigClusterFlag, "cluster", "", "Cluster name or ID")
	clusterKubeconfigRemoveCmd.Flags().BoolVar(&kubeconfigAllFlag, "all", false, "Remove all Ankra-managed contexts")
	clusterKubeconfigRemoveCmd.Flags().StringVar(&kubeconfigPathFlag, "kubeconfig", "", "Path to the kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")

	clusterKubeconfigListCmd.Flags().StringVar(&kubeconfigPathFlag, "kubeconfig", "", "Path to the kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")

	clusterKubeconfigCmd.AddCommand(clusterKubeconfigAddCmd)
	clusterKubeconfigCmd.AddCommand(clusterKubeconfigRemoveCmd)
	clusterKubeconfigCmd.AddCommand(clusterKubeconfigListCmd)
	clusterCmd.AddCommand(clusterKubeconfigCmd)
}
