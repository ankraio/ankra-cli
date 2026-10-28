// Package kubeconfig provides a small, dependency-light manager for the
// standard kubectl kubeconfig file. It can upsert, remove, and list the
// entries Ankra manages without disturbing any other clusters/users/contexts
// the user already has.
//
// Foreign entry bodies (other clusters, users, contexts) are preserved
// verbatim via yaml.Node so unknown fields, auth plugins, and CA data are
// never mangled. Top-level key order and comments are normalised the same way
// kubectl normalises a file when it writes to it.
package kubeconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManagedPrefix marks the cluster/user/context entries Ankra owns. It matches
// the naming the platform uses for portal-generated kubeconfigs, so a
// CLI-managed context and a downloaded one line up.
const ManagedPrefix = "ankra-"

// ExecAPIVersion is the client-go credential-plugin API version emitted in
// exec-based user entries. It must match what `ankra cluster kube-token`
// prints.
const ExecAPIVersion = "client.authentication.k8s.io/v1"

var slugPattern = regexp.MustCompile(`[^a-z0-9-]+`)

// Slugify mirrors the backend slug used for generated kubeconfig context names
// (lowercase, non [a-z0-9-] collapsed to '-', trimmed).
func Slugify(value string) string {
	slug := strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(value), "-"), "-")
	if slug == "" {
		return "cluster"
	}
	return slug
}

// ContextName returns the managed context/cluster/user name for a cluster name.
func ContextName(clusterName string) string {
	return ManagedPrefix + Slugify(clusterName)
}

// Config is a faithful-enough representation of a kubeconfig. The three named
// lists are addressed by name; their bodies are retained as raw nodes so
// foreign entries round-trip untouched. Any other top-level keys (preferences,
// extensions, ...) are preserved through Extra.
type Config struct {
	APIVersion     string               `yaml:"apiVersion,omitempty"`
	Kind           string               `yaml:"kind,omitempty"`
	Clusters       []NamedCluster       `yaml:"clusters"`
	Contexts       []NamedContext       `yaml:"contexts"`
	Users          []NamedUser          `yaml:"users"`
	CurrentContext string               `yaml:"current-context,omitempty"`
	Extra          map[string]yaml.Node `yaml:",inline"`
}

type NamedCluster struct {
	Name    string    `yaml:"name"`
	Cluster yaml.Node `yaml:"cluster"`
}

type NamedUser struct {
	Name string    `yaml:"name"`
	User yaml.Node `yaml:"user"`
}

type NamedContext struct {
	Name    string    `yaml:"name"`
	Context yaml.Node `yaml:"context"`
}

// Entry is a fully-built managed entry (one cluster, one user, one context that
// all share Name).
type Entry struct {
	Name    string
	Cluster yaml.Node
	User    yaml.Node
	Context yaml.Node
}

type clusterBody struct {
	Server                string `yaml:"server"`
	InsecureSkipTLSVerify bool   `yaml:"insecure-skip-tls-verify,omitempty"`
}

type contextBody struct {
	Cluster   string `yaml:"cluster"`
	User      string `yaml:"user"`
	Namespace string `yaml:"namespace,omitempty"`
}

// ExecConfig is the exec credential-plugin stanza.
type ExecConfig struct {
	APIVersion      string   `yaml:"apiVersion"`
	Command         string   `yaml:"command"`
	Args            []string `yaml:"args"`
	InteractiveMode string   `yaml:"interactiveMode"`
}

type execUser struct {
	Exec ExecConfig `yaml:"exec"`
}

type tokenUser struct {
	Token string `yaml:"token"`
}

// BuildExecEntry builds a managed entry whose credentials are fetched on demand
// by a credential plugin (the default, SSO-friendly mode). command is the
// executable kubectl invokes (defaults to "ankra" when empty).
func BuildExecEntry(name, server, command string, args []string, namespace string, insecure bool) (Entry, error) {
	if command == "" {
		command = "ankra"
	}
	user := execUser{Exec: ExecConfig{
		APIVersion:      ExecAPIVersion,
		Command:         command,
		Args:            args,
		InteractiveMode: "Never",
	}}
	return buildEntry(name, server, namespace, insecure, user)
}

// BuildTokenEntry builds a managed entry with a static (short-lived) bearer
// token embedded. Used for hosts where the ankra binary is not on PATH.
func BuildTokenEntry(name, server, token, namespace string, insecure bool) (Entry, error) {
	return buildEntry(name, server, namespace, insecure, tokenUser{Token: token})
}

func buildEntry(name, server, namespace string, insecure bool, userBody any) (Entry, error) {
	clusterNode, err := toNode(clusterBody{Server: server, InsecureSkipTLSVerify: insecure})
	if err != nil {
		return Entry{}, err
	}
	userNode, err := toNode(userBody)
	if err != nil {
		return Entry{}, err
	}
	contextNode, err := toNode(contextBody{Cluster: name, User: name, Namespace: namespace})
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: name, Cluster: clusterNode, User: userNode, Context: contextNode}, nil
}

func toNode(value any) (yaml.Node, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return yaml.Node{}, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return yaml.Node{}, err
	}
	if document.Kind == yaml.DocumentNode && len(document.Content) == 1 {
		return *document.Content[0], nil
	}
	return document, nil
}

// Upsert inserts the entry, replacing any existing cluster/user/context that
// shares its name.
func (config *Config) Upsert(entry Entry) {
	config.Clusters = upsertByName(config.Clusters, entry.Name,
		NamedCluster{Name: entry.Name, Cluster: entry.Cluster}, func(item NamedCluster) string { return item.Name })
	config.Users = upsertByName(config.Users, entry.Name,
		NamedUser{Name: entry.Name, User: entry.User}, func(item NamedUser) string { return item.Name })
	config.Contexts = upsertByName(config.Contexts, entry.Name,
		NamedContext{Name: entry.Name, Context: entry.Context}, func(item NamedContext) string { return item.Name })
}

// Remove deletes the cluster/user/context with the given name and clears
// current-context if it pointed at the removed context. It reports whether
// anything was removed.
func (config *Config) Remove(name string) bool {
	clusters, removedCluster := removeByName(config.Clusters, name, func(item NamedCluster) string { return item.Name })
	users, removedUser := removeByName(config.Users, name, func(item NamedUser) string { return item.Name })
	contexts, removedContext := removeByName(config.Contexts, name, func(item NamedContext) string { return item.Name })
	config.Clusters = clusters
	config.Users = users
	config.Contexts = contexts
	if config.CurrentContext == name {
		config.CurrentContext = ""
	}
	return removedCluster || removedUser || removedContext
}

// SetCurrentContext sets the active context.
func (config *Config) SetCurrentContext(name string) {
	config.CurrentContext = name
}

// ManagedContextNames returns the names of contexts that carry the managed
// prefix, in file order.
func (config *Config) ManagedContextNames() []string {
	var names []string
	for _, context := range config.Contexts {
		if strings.HasPrefix(context.Name, ManagedPrefix) {
			names = append(names, context.Name)
		}
	}
	return names
}

// ClusterServer returns the server URL of the named cluster, or "" if absent.
func (config *Config) ClusterServer(name string) string {
	for _, cluster := range config.Clusters {
		if cluster.Name == name {
			var body clusterBody
			if err := cluster.Cluster.Decode(&body); err == nil {
				return body.Server
			}
			return ""
		}
	}
	return ""
}

func upsertByName[T any](items []T, name string, replacement T, nameOf func(T) string) []T {
	for index := range items {
		if nameOf(items[index]) == name {
			items[index] = replacement
			return items
		}
	}
	return append(items, replacement)
}

func removeByName[T any](items []T, name string, nameOf func(T) string) ([]T, bool) {
	result := make([]T, 0, len(items))
	removed := false
	for _, item := range items {
		if nameOf(item) == name {
			removed = true
			continue
		}
		result = append(result, item)
	}
	return result, removed
}

// Load reads a kubeconfig from disk. A missing file yields an empty, valid
// config rather than an error, so callers can add the first entry.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{APIVersion: "v1", Kind: "Config"}, nil
	}
	if err != nil {
		return nil, err
	}
	config := &Config{}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("parse kubeconfig %s: %w", path, err)
	}
	if config.APIVersion == "" {
		config.APIVersion = "v1"
	}
	if config.Kind == "" {
		config.Kind = "Config"
	}
	return config, nil
}

// Marshal renders the config to YAML.
func Marshal(config *Config) ([]byte, error) {
	return yaml.Marshal(config)
}

// Save atomically writes the config to path with 0600 permissions, creating the
// parent directory if necessary.
func Save(path string, config *Config) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	data, err := Marshal(config)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".kubeconfig-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
