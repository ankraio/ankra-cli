package migrate

// The ImportCluster shape a module produces. It mirrors the YAML that
// `ankra cluster apply` already accepts (cmd/clone.go holds the reader side);
// the json tags are the module wire format and the yaml tags are what gets
// written to cluster.yaml, so the two stay in step by construction.

// Cluster is a complete ImportCluster manifest.
type Cluster struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind"       yaml:"kind"`
	Metadata   Metadata `json:"metadata"   yaml:"metadata"`
	Spec       Spec     `json:"spec"       yaml:"spec"`
}

// Metadata names the cluster.
type Metadata struct {
	Name        string `json:"name"                  yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Spec holds the stacks the cluster deploys.
type Spec struct {
	Stacks []Stack `json:"stacks" yaml:"stacks"`
}

// Stack is one deployable group of manifests and addons.
type Stack struct {
	Name        string     `json:"name"                  yaml:"name"`
	Description string     `json:"description,omitempty" yaml:"description,omitempty"`
	Manifests   []Manifest `json:"manifests,omitempty"   yaml:"manifests,omitempty"`
	Addons      []Addon    `json:"addons,omitempty"      yaml:"addons,omitempty"`
}

// Manifest is a raw Kubernetes manifest the stack applies. Parents encode
// deployment order, which is how a source format's dependency graph
// (compose's depends_on, for instance) survives the conversion.
type Manifest struct {
	Name      string   `json:"name"                yaml:"name"`
	FromFile  string   `json:"from_file,omitempty" yaml:"from_file,omitempty"`
	Namespace string   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Parents   []Parent `json:"parents,omitempty"   yaml:"parents,omitempty"`
}

// Addon is a Helm chart the stack installs.
type Addon struct {
	Name          string                 `json:"name"                     yaml:"name"`
	ChartName     string                 `json:"chart_name"               yaml:"chart_name"`
	ChartVersion  string                 `json:"chart_version"            yaml:"chart_version"`
	RepositoryURL string                 `json:"repository_url,omitempty" yaml:"repository_url,omitempty"`
	Namespace     string                 `json:"namespace,omitempty"      yaml:"namespace,omitempty"`
	Configuration map[string]interface{} `json:"configuration,omitempty"  yaml:"configuration,omitempty"`
	Parents       []Parent               `json:"parents,omitempty"        yaml:"parents,omitempty"`
}

// Parent is a dependency edge: this resource deploys after the named one.
type Parent struct {
	Name string `json:"name" yaml:"name"`
	Kind string `json:"kind" yaml:"kind"`
}

// Parent kinds accepted by the platform.
const (
	ParentKindManifest = "manifest"
	ParentKindAddon    = "addon"
)

// ImportCluster manifest identifiers.
const (
	APIVersion = "ankra.io/v1alpha1"
	KindImport = "ImportCluster"
)
