package docker

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"ankra/internal/migrate"
)

// RenderOptions shape the Kubernetes output.
type RenderOptions struct {
	ClusterName string
	Namespace   string
	// VolumeSize is the request for every PersistentVolumeClaim; compose has
	// no notion of size, so one value covers them all.
	VolumeSize   string
	StorageClass string
	// Ingress maps a workload name to the public host that should reach it.
	Ingress map[string]string
	// ClusterIssuer, when set, asks cert-manager for a certificate on every
	// Ingress.
	ClusterIssuer string
	// Images overrides the image for a workload, which is how a locally built
	// image gets pointed at the registry it was pushed to.
	Images map[string]string
}

// Render turns a Project into the Ankra resources for it.
func Render(project Project, options RenderOptions) migrate.Result {
	if options.VolumeSize == "" {
		options.VolumeSize = "10Gi"
	}
	if options.Namespace == "" {
		options.Namespace = sanitiseName(project.Name)
	}

	result := migrate.Result{Files: map[string]string{}}
	warn := func(format string, args ...interface{}) {
		result.Warnings = append(result.Warnings, fmt.Sprintf(format, args...))
	}

	stack := migrate.Stack{
		Name:        sanitiseName(project.Name),
		Description: "Converted from " + project.Source,
	}

	result.Files["manifests/namespace.yaml"] = renderObjects(namespaceObject(options.Namespace))
	stack.Manifests = append(stack.Manifests, migrate.Manifest{Name: "namespace", FromFile: "manifests/namespace.yaml"})

	workloads := project.SortedWorkloads()
	known := map[string]bool{}
	volumeUsers := map[string][]string{}
	for _, workload := range workloads {
		known[workload.Name] = true
		for _, volume := range workload.Volumes {
			if volume.Named {
				volumeUsers[volume.Name] = append(volumeUsers[volume.Name], workload.Name)
			}
		}
	}

	if len(volumeUsers) > 0 {
		var claims []interface{}
		for _, name := range sortedVolumeNames(volumeUsers) {
			claims = append(claims, claimObject(name, options))
			if users := volumeUsers[name]; len(users) > 1 {
				warn("volume %s is shared by %s; a ReadWriteOnce claim can only attach to one node, so they must be scheduled together or the volume split", name, strings.Join(users, " and "))
			}
		}
		result.Files["manifests/volumes.yaml"] = renderObjects(claims...)
		stack.Manifests = append(stack.Manifests, migrate.Manifest{
			Name: "volumes", FromFile: "manifests/volumes.yaml", Namespace: options.Namespace,
			Parents: []migrate.Parent{{Name: "namespace", Kind: migrate.ParentKindManifest}},
		})
	}

	for _, workload := range workloads {
		objects, workloadWarnings := renderWorkload(project, workload, options)
		result.Warnings = append(result.Warnings, workloadWarnings...)
		file := "manifests/" + workload.Name + ".yaml"
		result.Files[file] = renderObjects(objects...)

		manifest := migrate.Manifest{
			Name: workload.Name, FromFile: file, Namespace: options.Namespace,
			Parents: []migrate.Parent{{Name: "namespace", Kind: migrate.ParentKindManifest}},
		}
		if workload.Stateful {
			manifest.Parents = append(manifest.Parents, migrate.Parent{Name: "volumes", Kind: migrate.ParentKindManifest})
		}
		for _, dependency := range workload.DependsOn {
			if !known[dependency] {
				warn("%s depends on %s, which is not part of the conversion; the dependency was dropped", workload.Name, dependency)
				continue
			}
			manifest.Parents = append(manifest.Parents, migrate.Parent{Name: dependency, Kind: migrate.ParentKindManifest})
		}
		stack.Manifests = append(stack.Manifests, manifest)
	}

	result.Cluster = migrate.Cluster{
		APIVersion: migrate.APIVersion,
		Kind:       migrate.KindImport,
		Metadata:   migrate.Metadata{Name: options.ClusterName, Description: "Converted from " + project.Source},
		Spec:       migrate.Spec{Stacks: []migrate.Stack{stack}},
	}
	return result
}

func sortedVolumeNames(users map[string][]string) []string {
	names := make([]string, 0, len(users))
	for name := range users {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func renderWorkload(project Project, workload Workload, options RenderOptions) ([]interface{}, []string) {
	var objects []interface{}
	var warnings []string
	warn := func(format string, args ...interface{}) {
		warnings = append(warnings, workload.Name+": "+fmt.Sprintf(format, args...))
	}
	namespace := options.Namespace

	image := workload.Image
	if override, ok := options.Images[workload.Name]; ok {
		image = override
	} else if workload.Build != nil {
		if image == "" {
			image = sanitiseName(project.Name) + "-" + workload.Name + ":latest"
		}
		warn("image is built locally from %s; build it for the cluster's architecture, push it to a registry the cluster can pull from, and set --option image.%s=<registry>/<repo>:<tag>",
			path.Join(workload.Build.Context, workload.Build.Dockerfile), workload.Name)
	}

	config := map[string]string{}
	secrets := map[string]string{}
	for _, entry := range workload.Env {
		if entry.Unresolved {
			warn("%s has no value; set it in the generated ConfigMap or Secret before applying", entry.Name)
		}
		if entry.Secret {
			secrets[entry.Name] = entry.Value
		} else {
			config[entry.Name] = entry.Value
		}
	}

	var envFrom []envFromSource
	if len(config) > 0 {
		objects = append(objects, configMapObject(workload.Name+"-config", namespace, config))
		envFrom = append(envFrom, envFromSource{ConfigMapRef: &nameRef{Name: workload.Name + "-config"}})
	}
	if len(secrets) > 0 {
		objects = append(objects, secretObject(workload.Name+"-secrets", namespace, secrets))
		envFrom = append(envFrom, envFromSource{SecretRef: &nameRef{Name: workload.Name + "-secrets"}})
		warn("%d credential(s) were written to Secret %s-secrets in plain text; run `ankra cluster encrypt` before committing", len(secrets), workload.Name)
	}

	var mounts []volumeMount
	var podVolumes []podVolume
	files := map[string]string{}
	for _, volume := range workload.Volumes {
		switch {
		case volume.Named:
			mounts = append(mounts, volumeMount{Name: volume.Name, MountPath: volume.Target, ReadOnly: volume.ReadOnly})
			podVolumes = append(podVolumes, podVolume{Name: volume.Name, PersistentVolumeClaim: &nameRef{ClaimName: volume.Name}})
		case volume.BindFile:
			for fileName, content := range volume.HostFiles {
				files[fileName] = content
				mounts = append(mounts, volumeMount{Name: "files", MountPath: volume.Target, SubPath: fileName, ReadOnly: volume.ReadOnly})
			}
		case volume.BindDir:
			// Already warned by the reader; nothing can be mounted.
		default:
			scratch := sanitiseName(strings.Trim(volume.Target, "/"))
			mounts = append(mounts, volumeMount{Name: scratch, MountPath: volume.Target})
			podVolumes = append(podVolumes, podVolume{Name: scratch, EmptyDir: &struct{}{}})
		}
	}
	if len(files) > 0 {
		objects = append(objects, configMapObject(workload.Name+"-files", namespace, files))
		podVolumes = append(podVolumes, podVolume{Name: "files", ConfigMap: &nameRef{Name: workload.Name + "-files"}})
	}

	var containerPorts []containerPort
	var servicePorts []servicePort
	for _, port := range workload.Ports {
		protocol := strings.ToUpper(port.Protocol)
		containerPorts = append(containerPorts, containerPort{ContainerPort: port.Container, Protocol: protocol})
		servicePorts = append(servicePorts, servicePort{
			Name: fmt.Sprintf("%s-%d", port.Protocol, port.Container), Port: port.Container, TargetPort: port.Container, Protocol: protocol,
		})
		if port.Public && options.Ingress[workload.Name] == "" && port.Protocol == "tcp" {
			warn("publishes port %d on the host; add --option ingress.%s=<host> to expose it through an Ingress", port.Host, workload.Name)
		}
	}

	container := podContainer{
		Name:         workload.Name,
		Image:        image,
		Command:      workload.Command,
		Args:         workload.Args,
		EnvFrom:      envFrom,
		Ports:        containerPorts,
		VolumeMounts: mounts,
	}
	if workload.Healthcheck != nil {
		container.ReadinessProbe = probeFrom(*workload.Healthcheck)
	}
	if workload.Memory != "" || workload.CPU != "" {
		limits := map[string]string{}
		if workload.Memory != "" {
			limits["memory"] = workload.Memory
		}
		if workload.CPU != "" {
			limits["cpu"] = workload.CPU
		}
		container.Resources = &resources{Limits: limits, Requests: limits}
	}

	pod := podSpec{
		// Kubernetes injects <SERVICE>_PORT=tcp://... for every Service in the
		// namespace, and applications that read a variable of the same name
		// for their own configuration break on it. Nothing here discovers
		// peers that way, so switch the links off across the board.
		EnableServiceLinks: boolPointer(false),
		Containers:         []podContainer{container},
		Volumes:            podVolumes,
	}

	labels := map[string]string{"app": workload.Name}
	if isOneShot(workload) {
		pod.RestartPolicy = "OnFailure"
		objects = append(objects, jobObject(workload.Name, namespace, labels, pod))
	} else {
		deployment := deploymentObject(workload.Name, namespace, labels, pod)
		if workload.Stateful {
			// A single replica on ReadWriteOnce storage cannot roll: the new
			// pod would wait forever for a volume the old one still holds.
			deployment.Spec.(*deploymentSpec).Strategy = &deploymentStrategy{Type: "Recreate"}
		}
		objects = append(objects, deployment)
	}

	if len(servicePorts) > 0 {
		objects = append(objects, serviceObject(workload.Name, namespace, labels, servicePorts))
	}

	if host := options.Ingress[workload.Name]; host != "" {
		if len(servicePorts) == 0 {
			warn("an ingress host was given but the workload exposes no port")
		} else {
			objects = append(objects, ingressObject(workload.Name, namespace, host, servicePorts[0].Port, options.ClusterIssuer))
		}
	}

	return objects, warnings
}

// isOneShot decides between a Job and a long-running workload. Only an
// explicit restart policy of no/on-failure is trusted: a long-running
// process rendered as a Job exits once and is never restarted, silently,
// whereas a one-shot rendered as a Deployment crashloops where anyone can
// see it. When unsure, fail loudly.
func isOneShot(workload Workload) bool {
	switch workload.Restart {
	case "no", "on-failure":
		return true
	}
	return false
}

func probeFrom(health Healthcheck) *probe {
	var command []string
	switch {
	case len(health.Test) > 1 && health.Test[0] == "CMD-SHELL":
		command = []string{"sh", "-c", health.Test[1]}
	case len(health.Test) > 1 && health.Test[0] == "CMD":
		command = health.Test[1:]
	default:
		command = health.Test
	}
	result := &probe{Exec: &execAction{Command: command}}
	result.PeriodSeconds = health.IntervalSec
	result.TimeoutSeconds = health.TimeoutSec
	result.FailureThreshold = health.Retries
	result.InitialDelaySeconds = health.StartSec
	return result
}

func boolPointer(value bool) *bool { return &value }

// renderObjects serialises objects as a multi-document YAML stream.
func renderObjects(objects ...interface{}) string {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	for _, object := range objects {
		if err := encoder.Encode(object); err != nil {
			// Every object here is a plain struct; an encoding failure is a
			// programming error, not a user one.
			panic(err)
		}
	}
	_ = encoder.Close()
	return buffer.String()
}

// The Kubernetes object subset the conversion emits. Field order is the order
// in the rendered YAML, chosen to read like hand-written manifests.

type k8sObject struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   objectMeta        `yaml:"metadata"`
	Type       string            `yaml:"type,omitempty"`
	Data       map[string]string `yaml:"data,omitempty"`
	StringData map[string]string `yaml:"stringData,omitempty"`
	Spec       interface{}       `yaml:"spec,omitempty"`
}

type objectMeta struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

type nameRef struct {
	Name      string `yaml:"name,omitempty"`
	ClaimName string `yaml:"claimName,omitempty"`
}

type envFromSource struct {
	ConfigMapRef *nameRef `yaml:"configMapRef,omitempty"`
	SecretRef    *nameRef `yaml:"secretRef,omitempty"`
}

type containerPort struct {
	ContainerPort int    `yaml:"containerPort"`
	Protocol      string `yaml:"protocol,omitempty"`
}

type volumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	SubPath   string `yaml:"subPath,omitempty"`
	ReadOnly  bool   `yaml:"readOnly,omitempty"`
}

type podVolume struct {
	Name                  string    `yaml:"name"`
	PersistentVolumeClaim *nameRef  `yaml:"persistentVolumeClaim,omitempty"`
	ConfigMap             *nameRef  `yaml:"configMap,omitempty"`
	EmptyDir              *struct{} `yaml:"emptyDir,omitempty"`
}

type execAction struct {
	Command []string `yaml:"command"`
}

type probe struct {
	Exec                *execAction `yaml:"exec,omitempty"`
	InitialDelaySeconds int         `yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int         `yaml:"periodSeconds,omitempty"`
	TimeoutSeconds      int         `yaml:"timeoutSeconds,omitempty"`
	FailureThreshold    int         `yaml:"failureThreshold,omitempty"`
}

type resources struct {
	Requests map[string]string `yaml:"requests,omitempty"`
	Limits   map[string]string `yaml:"limits,omitempty"`
}

type podContainer struct {
	Name           string          `yaml:"name"`
	Image          string          `yaml:"image"`
	Command        []string        `yaml:"command,omitempty"`
	Args           []string        `yaml:"args,omitempty"`
	EnvFrom        []envFromSource `yaml:"envFrom,omitempty"`
	Ports          []containerPort `yaml:"ports,omitempty"`
	VolumeMounts   []volumeMount   `yaml:"volumeMounts,omitempty"`
	ReadinessProbe *probe          `yaml:"readinessProbe,omitempty"`
	Resources      *resources      `yaml:"resources,omitempty"`
}

type podSpec struct {
	RestartPolicy      string         `yaml:"restartPolicy,omitempty"`
	EnableServiceLinks *bool          `yaml:"enableServiceLinks,omitempty"`
	Containers         []podContainer `yaml:"containers"`
	Volumes            []podVolume    `yaml:"volumes,omitempty"`
}

type podTemplate struct {
	Metadata objectMeta `yaml:"metadata"`
	Spec     podSpec    `yaml:"spec"`
}

type labelSelector struct {
	MatchLabels map[string]string `yaml:"matchLabels"`
}

type deploymentStrategy struct {
	Type string `yaml:"type"`
}

type deploymentSpec struct {
	Replicas int                 `yaml:"replicas"`
	Selector labelSelector       `yaml:"selector"`
	Strategy *deploymentStrategy `yaml:"strategy,omitempty"`
	Template podTemplate         `yaml:"template"`
}

type jobSpec struct {
	BackoffLimit int         `yaml:"backoffLimit"`
	Template     podTemplate `yaml:"template"`
}

type servicePort struct {
	Name       string `yaml:"name"`
	Port       int    `yaml:"port"`
	TargetPort int    `yaml:"targetPort"`
	Protocol   string `yaml:"protocol,omitempty"`
}

type serviceSpec struct {
	Selector map[string]string `yaml:"selector"`
	Ports    []servicePort     `yaml:"ports"`
}

type claimSpec struct {
	AccessModes      []string       `yaml:"accessModes"`
	StorageClassName string         `yaml:"storageClassName,omitempty"`
	Resources        claimResources `yaml:"resources"`
}

type claimResources struct {
	Requests map[string]string `yaml:"requests"`
}

type ingressSpec struct {
	TLS   []ingressTLS  `yaml:"tls,omitempty"`
	Rules []ingressRule `yaml:"rules"`
}

type ingressTLS struct {
	Hosts      []string `yaml:"hosts"`
	SecretName string   `yaml:"secretName"`
}

type ingressRule struct {
	Host string      `yaml:"host"`
	HTTP ingressHTTP `yaml:"http"`
}

type ingressHTTP struct {
	Paths []ingressPath `yaml:"paths"`
}

type ingressPath struct {
	Path     string         `yaml:"path"`
	PathType string         `yaml:"pathType"`
	Backend  ingressBackend `yaml:"backend"`
}

type ingressBackend struct {
	Service ingressService `yaml:"service"`
}

type ingressService struct {
	Name string             `yaml:"name"`
	Port ingressServicePort `yaml:"port"`
}

type ingressServicePort struct {
	Number int `yaml:"number"`
}

func namespaceObject(name string) k8sObject {
	return k8sObject{APIVersion: "v1", Kind: "Namespace", Metadata: objectMeta{Name: name}}
}

func configMapObject(name, namespace string, data map[string]string) k8sObject {
	return k8sObject{APIVersion: "v1", Kind: "ConfigMap", Metadata: objectMeta{Name: name, Namespace: namespace}, Data: data}
}

func secretObject(name, namespace string, data map[string]string) k8sObject {
	return k8sObject{APIVersion: "v1", Kind: "Secret", Metadata: objectMeta{Name: name, Namespace: namespace}, Type: "Opaque", StringData: data}
}

func claimObject(name string, options RenderOptions) k8sObject {
	return k8sObject{
		APIVersion: "v1", Kind: "PersistentVolumeClaim",
		Metadata: objectMeta{Name: name, Namespace: options.Namespace},
		Spec: claimSpec{
			AccessModes:      []string{"ReadWriteOnce"},
			StorageClassName: options.StorageClass,
			Resources:        claimResources{Requests: map[string]string{"storage": options.VolumeSize}},
		},
	}
}

func serviceObject(name, namespace string, labels map[string]string, ports []servicePort) k8sObject {
	return k8sObject{
		APIVersion: "v1", Kind: "Service",
		Metadata: objectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec:     serviceSpec{Selector: labels, Ports: ports},
	}
}

func deploymentObject(name, namespace string, labels map[string]string, pod podSpec) k8sObject {
	return k8sObject{
		APIVersion: "apps/v1", Kind: "Deployment",
		Metadata: objectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: &deploymentSpec{
			Replicas: 1,
			Selector: labelSelector{MatchLabels: labels},
			Template: podTemplate{Metadata: objectMeta{Labels: labels}, Spec: pod},
		},
	}
}

func jobObject(name, namespace string, labels map[string]string, pod podSpec) k8sObject {
	return k8sObject{
		APIVersion: "batch/v1", Kind: "Job",
		Metadata: objectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: jobSpec{
			BackoffLimit: 3,
			Template:     podTemplate{Metadata: objectMeta{Labels: labels}, Spec: pod},
		},
	}
}

func ingressObject(name, namespace, host string, port int, clusterIssuer string) k8sObject {
	object := k8sObject{
		APIVersion: "networking.k8s.io/v1", Kind: "Ingress",
		Metadata: objectMeta{Name: name, Namespace: namespace},
		Spec: ingressSpec{
			Rules: []ingressRule{{Host: host, HTTP: ingressHTTP{Paths: []ingressPath{{
				Path: "/", PathType: "Prefix",
				Backend: ingressBackend{Service: ingressService{Name: name, Port: ingressServicePort{Number: port}}},
			}}}}},
		},
	}
	if clusterIssuer != "" {
		object.Metadata.Annotations = map[string]string{"cert-manager.io/cluster-issuer": clusterIssuer}
		spec := object.Spec.(ingressSpec)
		spec.TLS = []ingressTLS{{Hosts: []string{host}, SecretName: name + "-tls"}}
		object.Spec = spec
	}
	return object
}
