package docker

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ComposeFileNames are searched in the order the compose CLI itself uses.
var ComposeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// FindComposeFile returns the first compose file in dir.
func FindComposeFile(dir string) (string, bool) {
	for _, name := range ComposeFileNames {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

// ComposeOptions control how a compose file is read.
type ComposeOptions struct {
	// Profiles selects which profiled services to include. Services with no
	// profile are always included, matching `docker compose up`.
	Profiles []string
	// AllProfiles includes every service regardless of profile.
	AllProfiles bool
	// UseEnvironment lets the process environment satisfy ${VAR} references,
	// the way the compose CLI does. Off by default: a migration that silently
	// bakes the operator's shell into generated manifests is a trap.
	UseEnvironment bool
}

type composeFile struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]yaml.Node      `yaml:"volumes"`
}

type composeService struct {
	Image       string         `yaml:"image"`
	Build       yaml.Node      `yaml:"build"`
	Command     yaml.Node      `yaml:"command"`
	Entrypoint  yaml.Node      `yaml:"entrypoint"`
	Environment yaml.Node      `yaml:"environment"`
	EnvFile     yaml.Node      `yaml:"env_file"`
	Ports       []yaml.Node    `yaml:"ports"`
	Volumes     []yaml.Node    `yaml:"volumes"`
	DependsOn   yaml.Node      `yaml:"depends_on"`
	Healthcheck *composeHealth `yaml:"healthcheck"`
	Restart     string         `yaml:"restart"`
	MemLimit    yaml.Node      `yaml:"mem_limit"`
	CPUs        yaml.Node      `yaml:"cpus"`
	Profiles    []string       `yaml:"profiles"`
	Deploy      *composeDeploy `yaml:"deploy"`
}

type composeHealth struct {
	Test        yaml.Node `yaml:"test"`
	Interval    string    `yaml:"interval"`
	Timeout     string    `yaml:"timeout"`
	Retries     yaml.Node `yaml:"retries"`
	StartPeriod string    `yaml:"start_period"`
	Disable     bool      `yaml:"disable"`
}

type composeDeploy struct {
	Resources struct {
		Limits struct {
			Memory yaml.Node `yaml:"memory"`
			CPUs   yaml.Node `yaml:"cpus"`
		} `yaml:"limits"`
	} `yaml:"resources"`
}

type composeLongPort struct {
	Target    int       `yaml:"target"`
	Published yaml.Node `yaml:"published"`
	Protocol  string    `yaml:"protocol"`
	HostIP    string    `yaml:"host_ip"`
}

type composeLongVolume struct {
	Type     string `yaml:"type"`
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only"`
}

// LoadCompose reads a compose file into a Project. Warnings describe what
// could not be carried over; they are returned rather than failing because a
// partial conversion a human can finish beats none.
func LoadCompose(dir, path string, options ComposeOptions) (Project, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Project{}, nil, err
	}

	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return Project{}, nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}

	environment := map[string]string{}
	loadEnvFile(filepath.Join(dir, ".env"), environment)
	lookup := func(name string) (string, bool) {
		if value, ok := environment[name]; ok {
			return value, true
		}
		if options.UseEnvironment {
			return os.LookupEnv(name)
		}
		return "", false
	}

	unresolved := map[*yaml.Node][]string{}
	interpolateNode(&document, lookup, unresolved)

	var file composeFile
	if err := document.Decode(&file); err != nil {
		return Project{}, nil, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}

	project := Project{
		Name:   file.Name,
		Source: filepath.Base(path),
	}
	if project.Name == "" {
		project.Name = filepath.Base(dir)
	}
	for name := range file.Volumes {
		project.Volumes = append(project.Volumes, name)
	}
	sort.Strings(project.Volumes)

	var warnings []string
	names := make([]string, 0, len(file.Services))
	for name := range file.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	selected := map[string]bool{}
	for _, profile := range options.Profiles {
		selected[profile] = true
	}

	for _, name := range names {
		service := file.Services[name]
		if !options.AllProfiles && len(service.Profiles) > 0 && !anyProfileSelected(service.Profiles, selected) {
			warnings = append(warnings, fmt.Sprintf(
				"%s: skipped, it belongs to profile %s (pass --option profiles=%s to include it)",
				name, strings.Join(service.Profiles, ","), service.Profiles[0]))
			continue
		}
		workload, serviceWarnings := composeWorkload(dir, name, service, lookup, unresolved)
		workload.Profiles = service.Profiles
		project.Workloads = append(project.Workloads, workload)
		warnings = append(warnings, serviceWarnings...)
	}

	return project, warnings, nil
}

func anyProfileSelected(profiles []string, selected map[string]bool) bool {
	for _, profile := range profiles {
		if selected[profile] {
			return true
		}
	}
	return false
}

func composeWorkload(dir, name string, service composeService, lookup func(string) (string, bool), unresolved map[*yaml.Node][]string) (Workload, []string) {
	var warnings []string
	warn := func(format string, args ...interface{}) {
		warnings = append(warnings, name+": "+fmt.Sprintf(format, args...))
	}

	workload := Workload{
		Name:    name,
		Image:   service.Image,
		Restart: service.Restart,
	}

	if build, ok := composeBuild(service.Build); ok {
		workload.Build = &build
	}

	workload.Command = stringList(service.Entrypoint)
	workload.Args = stringList(service.Command)

	// env_file entries come first so that `environment` overrides them, the
	// same precedence compose applies.
	for _, envPath := range stringList(service.EnvFile) {
		fileValues := map[string]string{}
		if !loadEnvFile(filepath.Join(dir, envPath), fileValues) {
			warn("env_file %s not found", envPath)
			continue
		}
		for _, key := range sortedKeys(fileValues) {
			workload.Env = upsertEnv(workload.Env, EnvVar{Name: key, Value: fileValues[key], Secret: looksSecret(key, fileValues[key])})
		}
	}
	for _, entry := range composeEnvironment(service.Environment, lookup, unresolved) {
		workload.Env = upsertEnv(workload.Env, entry)
	}

	for _, node := range service.Ports {
		port, ok, reason := composePort(node)
		if !ok {
			warn("port %s", reason)
			continue
		}
		workload.Ports = append(workload.Ports, port)
	}

	for _, node := range service.Volumes {
		volume, ok, reason := composeVolume(dir, node)
		if !ok {
			warn("volume %s", reason)
			continue
		}
		if volume.BindDir {
			warn("bind mount %s is a host directory; it cannot be carried into the cluster - mount a PersistentVolumeClaim at %s and copy the data across", volume.Source, volume.Target)
		}
		if volume.Named {
			workload.Stateful = true
		}
		workload.Volumes = append(workload.Volumes, volume)
	}

	workload.DependsOn = composeDependsOn(service.DependsOn)

	if service.Healthcheck != nil && !service.Healthcheck.Disable {
		if health, ok := composeHealthcheck(*service.Healthcheck); ok {
			workload.Healthcheck = &health
		}
	}

	workload.Memory = normaliseMemory(scalarString(service.MemLimit))
	workload.CPU = normaliseCPU(scalarString(service.CPUs))
	if service.Deploy != nil {
		if memory := normaliseMemory(scalarString(service.Deploy.Resources.Limits.Memory)); memory != "" {
			workload.Memory = memory
		}
		if cpu := normaliseCPU(scalarString(service.Deploy.Resources.Limits.CPUs)); cpu != "" {
			workload.CPU = cpu
		}
	}

	if workload.Image == "" && workload.Build == nil {
		warn("has neither image nor build; the generated workload has no image to run")
	}

	return workload, warnings
}

func composeBuild(node yaml.Node) (Build, bool) {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value == "" {
			return Build{}, false
		}
		return Build{Context: node.Value, Dockerfile: "Dockerfile"}, true
	case yaml.MappingNode:
		var build struct {
			Context    string `yaml:"context"`
			Dockerfile string `yaml:"dockerfile"`
		}
		if err := node.Decode(&build); err != nil {
			return Build{}, false
		}
		if build.Context == "" {
			build.Context = "."
		}
		if build.Dockerfile == "" {
			build.Dockerfile = "Dockerfile"
		}
		return Build{Context: build.Context, Dockerfile: build.Dockerfile}, true
	}
	return Build{}, false
}

func composeEnvironment(node yaml.Node, lookup func(string) (string, bool), unresolved map[*yaml.Node][]string) []EnvVar {
	var out []EnvVar
	switch node.Kind {
	case yaml.SequenceNode:
		for _, item := range node.Content {
			key, value, hasValue := strings.Cut(item.Value, "=")
			entry := EnvVar{Name: key, Value: value}
			if !hasValue {
				// A bare KEY takes its value from the environment.
				if resolved, ok := lookup(key); ok {
					entry.Value = resolved
				} else {
					entry.Unresolved = true
				}
			}
			entry.Unresolved = entry.Unresolved || len(unresolved[item]) > 0
			entry.Secret = looksSecret(entry.Name, entry.Value)
			out = append(out, entry)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode, valueNode := node.Content[i], node.Content[i+1]
			entry := EnvVar{Name: keyNode.Value, Value: valueNode.Value}
			if valueNode.Tag == "!!null" {
				if resolved, ok := lookup(keyNode.Value); ok {
					entry.Value = resolved
				} else {
					entry.Unresolved = true
				}
			}
			entry.Unresolved = entry.Unresolved || len(unresolved[valueNode]) > 0
			entry.Secret = looksSecret(entry.Name, entry.Value)
			out = append(out, entry)
		}
	}
	return out
}

func composePort(node yaml.Node) (Port, bool, string) {
	if node.Kind == yaml.MappingNode {
		var long composeLongPort
		if err := node.Decode(&long); err != nil {
			return Port{}, false, "has an unreadable long-form entry"
		}
		published, _ := strconv.Atoi(scalarString(long.Published))
		protocol := long.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		return Port{Container: long.Target, Host: published, Protocol: protocol, Public: isPublicHostIP(long.HostIP) && published != 0}, true, ""
	}

	spec := node.Value
	protocol := "tcp"
	if base, proto, found := strings.Cut(spec, "/"); found {
		spec, protocol = base, proto
	}
	if strings.Contains(spec, "-") {
		return Port{}, false, fmt.Sprintf("range %q is not supported; declare each port separately", node.Value)
	}
	parts := strings.Split(spec, ":")
	var port Port
	port.Protocol = protocol
	switch len(parts) {
	case 1:
		port.Container, _ = strconv.Atoi(parts[0])
	case 2:
		port.Host, _ = strconv.Atoi(parts[0])
		port.Container, _ = strconv.Atoi(parts[1])
		port.Public = true
	case 3:
		port.Host, _ = strconv.Atoi(parts[1])
		port.Container, _ = strconv.Atoi(parts[2])
		port.Public = isPublicHostIP(parts[0])
	default:
		return Port{}, false, fmt.Sprintf("%q could not be parsed", node.Value)
	}
	if port.Container == 0 {
		return Port{}, false, fmt.Sprintf("%q has no container port", node.Value)
	}
	return port, true, ""
}

func isPublicHostIP(hostIP string) bool {
	switch hostIP {
	case "", "0.0.0.0", "::":
		return true
	}
	return false
}

func composeVolume(dir string, node yaml.Node) (Volume, bool, string) {
	var volume Volume
	if node.Kind == yaml.MappingNode {
		var long composeLongVolume
		if err := node.Decode(&long); err != nil {
			return Volume{}, false, "has an unreadable long-form entry"
		}
		volume = Volume{Source: long.Source, Target: long.Target, ReadOnly: long.ReadOnly}
		switch long.Type {
		case "volume":
			volume.Named = true
			volume.Name = long.Source
		case "bind":
			classifyBind(dir, &volume)
		case "tmpfs":
			volume.Source = ""
		default:
			return Volume{}, false, fmt.Sprintf("type %q is not supported", long.Type)
		}
		return volume, true, ""
	}

	parts := strings.Split(node.Value, ":")
	switch len(parts) {
	case 1:
		return Volume{Target: parts[0]}, true, ""
	case 2, 3:
		volume = Volume{Source: parts[0], Target: parts[1]}
		if len(parts) == 3 && strings.Contains(parts[2], "ro") {
			volume.ReadOnly = true
		}
	default:
		return Volume{}, false, fmt.Sprintf("%q could not be parsed", node.Value)
	}
	if strings.HasPrefix(volume.Source, ".") || strings.HasPrefix(volume.Source, "/") || strings.HasPrefix(volume.Source, "~") {
		classifyBind(dir, &volume)
		return volume, true, ""
	}
	volume.Named = true
	volume.Name = volume.Source
	return volume, true, ""
}

// classifyBind decides whether a bind mount is a single file - which can ride
// along as a ConfigMap - or a directory, which cannot.
func classifyBind(dir string, volume *Volume) {
	source := volume.Source
	if strings.HasPrefix(source, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			source = home + source[1:]
		}
	}
	if !filepath.IsAbs(source) {
		source = filepath.Join(dir, source)
	}
	info, err := os.Stat(source)
	if err != nil || info.IsDir() {
		volume.BindDir = true
		return
	}
	const maxFileSize = 1 << 20
	if info.Size() > maxFileSize {
		volume.BindDir = true
		return
	}
	content, err := os.ReadFile(source)
	if err != nil {
		volume.BindDir = true
		return
	}
	volume.BindFile = true
	volume.HostFiles = map[string]string{filepath.Base(source): string(content)}
}

func composeDependsOn(node yaml.Node) []string {
	var out []string
	switch node.Kind {
	case yaml.SequenceNode:
		for _, item := range node.Content {
			out = append(out, item.Value)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			out = append(out, node.Content[i].Value)
		}
	}
	sort.Strings(out)
	return out
}

func composeHealthcheck(health composeHealth) (Healthcheck, bool) {
	test := stringList(health.Test)
	if len(test) == 0 {
		return Healthcheck{}, false
	}
	if health.Test.Kind == yaml.ScalarNode {
		test = []string{"CMD-SHELL", health.Test.Value}
	}
	out := Healthcheck{
		Test:        test,
		IntervalSec: durationSeconds(health.Interval),
		TimeoutSec:  durationSeconds(health.Timeout),
		StartSec:    durationSeconds(health.StartPeriod),
	}
	out.Retries, _ = strconv.Atoi(scalarString(health.Retries))
	return out, true
}

func durationSeconds(value string) int {
	if value == "" {
		return 0
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return int(duration.Seconds())
}

// stringList reads a compose field that may be a string, a list, or absent.
func stringList(node yaml.Node) []string {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value == "" {
			return nil
		}
		return []string{node.Value}
	case yaml.SequenceNode:
		out := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			out = append(out, item.Value)
		}
		return out
	}
	return nil
}

func scalarString(node yaml.Node) string {
	if node.Kind == yaml.ScalarNode {
		return node.Value
	}
	return ""
}

func upsertEnv(env []EnvVar, entry EnvVar) []EnvVar {
	for i := range env {
		if env[i].Name == entry.Name {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var secretNamePattern = regexp.MustCompile(`(?i)(PASSWORD|PASSWD|SECRET|TOKEN|_KEY$|_KEY_|APIKEY|CREDENTIAL|PRIVATE)`)
var embeddedCredentialPattern = regexp.MustCompile(`://[^/:@\s]+:[^@\s]+@`)

// looksSecret guesses whether an environment entry is a credential. Names
// carry most of the signal; the value check catches connection strings that
// embed a password behind an innocent name like DATABASE_URL.
func looksSecret(name, value string) bool {
	return secretNamePattern.MatchString(name) || embeddedCredentialPattern.MatchString(value)
}

// loadEnvFile merges KEY=VALUE lines into values. It reports whether the file
// existed so callers can tell "empty" from "missing".
func loadEnvFile(path string, values map[string]string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		values[strings.TrimSpace(key)] = value
	}
	return true
}

var interpolationPattern = regexp.MustCompile(`\$(?:\$|\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?+])([^}]*))?\}|([A-Za-z_][A-Za-z0-9_]*))`)

// interpolate expands compose variable references and returns the names it
// could not satisfy.
func interpolate(raw string, lookup func(string) (string, bool)) (string, []string) {
	var unresolved []string
	expanded := interpolationPattern.ReplaceAllStringFunc(raw, func(match string) string {
		if match == "$$" {
			return "$"
		}
		groups := interpolationPattern.FindStringSubmatch(match)
		name := groups[1]
		if name == "" {
			name = groups[4]
		}
		operator, fallback := groups[2], groups[3]
		value, ok := lookup(name)
		switch operator {
		case "-":
			if !ok {
				return fallback
			}
		case ":-":
			if !ok || value == "" {
				return fallback
			}
		case "?", ":?":
			if !ok || (operator == ":?" && value == "") {
				unresolved = append(unresolved, name)
				return ""
			}
		case "+":
			if ok {
				return fallback
			}
			return ""
		case ":+":
			if ok && value != "" {
				return fallback
			}
			return ""
		}
		if !ok {
			unresolved = append(unresolved, name)
			return ""
		}
		return value
	})
	return expanded, unresolved
}

// interpolateNode expands references in every scalar of a parsed document.
// A scalar that held a reference is re-tagged as plain so a ${PORT:-3000}
// that became 3000 decodes as a number rather than the string it was parsed
// as.
func interpolateNode(node *yaml.Node, lookup func(string) (string, bool), unresolved map[*yaml.Node][]string) {
	switch node.Kind {
	case yaml.ScalarNode:
		if !strings.Contains(node.Value, "$") {
			return
		}
		value, missing := interpolate(node.Value, lookup)
		if node.Style == yaml.DoubleQuotedStyle || node.Style == yaml.SingleQuotedStyle {
			node.Value = value
		} else {
			node.Value = value
			node.Tag = ""
			node.Style = 0
		}
		if len(missing) > 0 {
			unresolved[node] = missing
		}
	default:
		for _, child := range node.Content {
			interpolateNode(child, lookup, unresolved)
		}
	}
}

// normaliseMemory turns compose's 2g / 512m / 1073741824 into the Kubernetes
// quantity form.
func normaliseMemory(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	value = strings.TrimSuffix(value, "b")
	if bytes, err := strconv.ParseInt(value, 10, 64); err == nil {
		return strconv.FormatInt(bytes/(1<<20), 10) + "Mi"
	}
	unit := value[len(value)-1]
	number := value[:len(value)-1]
	if _, err := strconv.ParseFloat(number, 64); err != nil {
		return ""
	}
	switch unit {
	case 'k':
		return number + "Ki"
	case 'm':
		return number + "Mi"
	case 'g':
		return number + "Gi"
	}
	return ""
}

// normaliseCPU turns compose's fractional cpus into Kubernetes millicores
// where needed.
func normaliseCPU(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cpus, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return ""
	}
	if cpus == float64(int(cpus)) {
		return strconv.Itoa(int(cpus))
	}
	return strconv.Itoa(int(cpus*1000)) + "m"
}
