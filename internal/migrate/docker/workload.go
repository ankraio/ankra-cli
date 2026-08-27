// Package docker converts Docker deployments to Ankra resources. Three sources
// are read - compose files, a bare Dockerfile, and the live daemon - and all
// three normalise into the Workload shape below before any Kubernetes YAML is
// generated, so the rendering path exists exactly once.
package docker

import "sort"

// Workload is the source-neutral description of one container the rendering
// step turns into Kubernetes objects.
type Workload struct {
	Name    string
	Image   string
	Command []string
	Args    []string
	Env     []EnvVar
	Ports   []Port
	Volumes []Volume
	// DependsOn are the names of workloads this one must start after; they
	// become manifest parents in the generated stack.
	DependsOn []string
	// Healthcheck, when present, becomes a readiness probe.
	Healthcheck *Healthcheck
	// Stateful marks a workload that owns a persistent volume: it renders as a
	// StatefulSet so its storage follows it across restarts.
	Stateful bool
	Memory   string
	CPU      string
	Restart  string
	// Build is set when the source builds the image locally rather than
	// pulling it. The image then has to be pushed somewhere the cluster can
	// reach, which the conversion cannot do for the user.
	Build *Build
	// Profiles are compose profiles; a workload with any profile is opt-in.
	Profiles []string
}

// EnvVar is one environment entry. Secret is a heuristic on the name: values
// that look like credentials go to a Secret rather than a ConfigMap.
type EnvVar struct {
	Name   string
	Value  string
	Secret bool
	// Unresolved is set when the value referenced a variable the source could
	// not supply (a compose ${VAR} with no default and no .env entry).
	Unresolved bool
}

// Port is a published or exposed container port.
type Port struct {
	Container int
	Host      int
	Protocol  string
	// Public is true when the source published the port on a host interface
	// other than loopback: it is the signal for an Ingress or a LoadBalancer.
	Public bool
}

// Volume is a mount. Named volumes become PersistentVolumeClaims; bind
// mounts of a file become ConfigMap entries; bind mounts of a directory
// cannot be carried over and produce a warning.
type Volume struct {
	Name      string
	Source    string
	Target    string
	ReadOnly  bool
	Named     bool
	BindFile  bool
	BindDir   bool
	HostFiles map[string]string
}

// Healthcheck is a container health probe.
type Healthcheck struct {
	Test        []string
	IntervalSec int
	TimeoutSec  int
	Retries     int
	StartSec    int
}

// Build describes a locally built image.
type Build struct {
	Context    string
	Dockerfile string
}

// Project is a set of workloads plus what the source knew about them as a
// whole: the named volumes declared, and which profiles were seen.
type Project struct {
	Name      string
	Workloads []Workload
	Volumes   []string
	Source    string
}

// SortedWorkloads returns the workloads in a deterministic order so the
// generated files are stable across runs - a conversion that reorders its
// output on every invocation is unreviewable in a pull request.
func (p Project) SortedWorkloads() []Workload {
	out := make([]Workload, len(p.Workloads))
	copy(out, p.Workloads)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
