package cmd

// The route check behind ship's Live line (ankra-4wl92c). A hostname on an
// Ingress is not a URL that works: the bake-off printed
// "Live: https://bakeoff-conductor-hub..." for an Ingress whose backend named
// Service bakeoff-conductor-hub while the release's Service was conductor-hub,
// so every request to the "live" URL failed. Before ship claims Live it reads
// the Ingress rules that carry the host and proves each backend Service
// exists, exposes the port the Ingress asks for, and has a ready endpoint.
//
// The check refuses only on proof. A read that fails, or a host no Ingress in
// the namespace carries (a route served some other way), is said out loud and
// leaves the Live line as it was: "could not check" is a different fact from
// "does not route".

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"
)

// shipResourceReadFailed is the platform's status for a resource read that
// failed (cluster usecase/kubeagent GetResourcesResponseItem): its empty item
// list is not an empty namespace.
const shipResourceReadFailed = "error"

// Kubernetes updates EndpointSlices a moment after a pod turns Ready, so a
// healthy deploy can read zero ready endpoints for a short while. Only that
// condition is transient (a missing Service or port is not), so it is re-read
// for up to shipEndpointSettleWindow before it is reported. Variables so tests
// can inject the sleeper and finish instantly.
var (
	shipEndpointSettleWindow   = 30 * time.Second
	shipEndpointSettleInterval = 2 * time.Second
	shipEndpointSettleSleep    = func(waitContext context.Context, interval time.Duration) bool {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		select {
		case <-waitContext.Done():
			return false
		case <-timer.C:
			return true
		}
	}
)

// shipIngressBackend is one Service an Ingress rule for the host sends traffic
// to, with the port it asks for by number or by name.
type shipIngressBackend struct {
	ingressName string
	serviceName string
	portNumber  string
	portName    string
}

// shipServicePort is one port a Service exposes.
type shipServicePort struct {
	name   string
	number string
}

// shipServiceView is the slice of a Service the route check reads.
type shipServiceView struct {
	serviceType string
	ports       []shipServicePort
}

// readShipNamespaceKind lists one kind in the namespace, live rather than from
// the platform's cache (a Service the deploy created seconds ago may not be
// cached yet, and "missing" must never come from a stale cache). ok is false
// when the read failed, so the caller cannot mistake it for an empty list.
func readShipNamespaceKind(clusterID string, request client.ResourceRequestItem) ([]map[string]interface{}, string, bool) {
	response, readError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{request},
		SkipCache:        true,
	})
	if readError != nil {
		return nil, readError.Error(), false
	}
	if response == nil || len(response.ResourceResponses) == 0 {
		return nil, "the platform answered no result", false
	}
	result := response.ResourceResponses[0]
	if result.Status == shipResourceReadFailed {
		reason := "the read failed"
		if result.ErrorResponse != nil && strings.TrimSpace(*result.ErrorResponse) != "" {
			reason = strings.TrimSpace(*result.ErrorResponse)
		}
		return nil, reason, false
	}
	objects := make([]map[string]interface{}, 0, len(result.Items))
	for _, item := range result.Items {
		if object, isObject := item.(map[string]interface{}); isObject {
			objects = append(objects, object)
		}
	}
	return objects, "", true
}

// shipIngressBackendsForHost answers the Service backends of every Ingress
// rule in the namespace whose host is the one ship would print. A rule with no
// paths uses the Ingress's default backend. Backends that are not Services
// (resource backends) are not checked.
func shipIngressBackendsForHost(ingresses []map[string]interface{}, host string) ([]shipIngressBackend, bool) {
	backends := []shipIngressBackend{}
	hostFound := false
	for _, ingress := range ingresses {
		ingressName := getNestedString(ingress, "metadata", "name")
		specification, hasSpecification := getNestedMap(ingress, "spec")
		if !hasSpecification {
			continue
		}
		rules, _ := specification["rules"].([]interface{})
		for _, rule := range rules {
			ruleObject, isRuleObject := rule.(map[string]interface{})
			if !isRuleObject || !strings.EqualFold(getNestedString(ruleObject, "host"), host) {
				continue
			}
			hostFound = true
			paths := []interface{}{}
			if httpRule, hasHTTP := getNestedMap(ruleObject, "http"); hasHTTP {
				paths, _ = httpRule["paths"].([]interface{})
			}
			if len(paths) == 0 {
				if backend, ok := shipServiceBackend(ingressName, specification, "defaultBackend"); ok {
					backends = append(backends, backend)
				}
				continue
			}
			for _, path := range paths {
				pathObject, isPathObject := path.(map[string]interface{})
				if !isPathObject {
					continue
				}
				if backend, ok := shipServiceBackend(ingressName, pathObject, "backend"); ok {
					backends = append(backends, backend)
				}
			}
		}
	}
	return backends, hostFound
}

func shipServiceBackend(ingressName string, holder map[string]interface{}, key string) (shipIngressBackend, bool) {
	backend, hasBackend := getNestedMap(holder, key)
	if !hasBackend {
		return shipIngressBackend{}, false
	}
	serviceName := getNestedString(backend, "service", "name")
	if serviceName == "" {
		return shipIngressBackend{}, false
	}
	return shipIngressBackend{
		ingressName: ingressName,
		serviceName: serviceName,
		portNumber:  getNestedString(backend, "service", "port", "number"),
		portName:    getNestedString(backend, "service", "port", "name"),
	}, true
}

// shipServicesByName indexes the namespace's Services by name.
func shipServicesByName(services []map[string]interface{}) map[string]shipServiceView {
	byName := map[string]shipServiceView{}
	for _, service := range services {
		name := getNestedString(service, "metadata", "name")
		if name == "" {
			continue
		}
		view := shipServiceView{serviceType: getNestedString(service, "spec", "type")}
		if specification, hasSpecification := getNestedMap(service, "spec"); hasSpecification {
			ports, _ := specification["ports"].([]interface{})
			for _, port := range ports {
				if portObject, isPortObject := port.(map[string]interface{}); isPortObject {
					view.ports = append(view.ports, shipServicePort{
						name:   getNestedString(portObject, "name"),
						number: getNestedString(portObject, "port"),
					})
				}
			}
		}
		byName[name] = view
	}
	return byName
}

// shipServiceExposesPort reports whether the Service has the port the Ingress
// backend asks for. A backend naming no port is not judged.
func shipServiceExposesPort(service shipServiceView, backend shipIngressBackend) bool {
	if backend.portNumber == "" && backend.portName == "" {
		return true
	}
	for _, port := range service.ports {
		if backend.portNumber != "" && port.number == backend.portNumber {
			return true
		}
		if backend.portName != "" && port.name == backend.portName {
			return true
		}
	}
	return false
}

// shipReadyEndpointCount counts the ready endpoints of a Service across its
// EndpointSlices. A missing ready condition counts as ready, as the
// EndpointSlice API defines it.
func shipReadyEndpointCount(slices []map[string]interface{}) int {
	ready := 0
	for _, slice := range slices {
		endpoints, _ := slice["endpoints"].([]interface{})
		for _, endpoint := range endpoints {
			endpointObject, isEndpointObject := endpoint.(map[string]interface{})
			if !isEndpointObject {
				continue
			}
			if getNestedString(endpointObject, "conditions", "ready") == "false" {
				continue
			}
			ready++
		}
	}
	return ready
}

// shipBackendPort renders the port a backend asks for, for messages.
func shipBackendPort(backend shipIngressBackend) string {
	if backend.portNumber != "" {
		return "port " + backend.portNumber
	}
	return "port " + backend.portName
}

// verifyShipURLRoutes answers what stops the host from routing, or "" when
// every backend the host's Ingress rules name resolves, or when that could not
// be checked (the reason is printed to progress).
func verifyShipURLRoutes(waitContext context.Context, progress io.Writer, clusterID string, namespace string, host string) string {
	ingresses, ingressReadProblem, ingressesRead := readShipNamespaceKind(clusterID, client.ResourceRequestItem{
		Kind: "Ingress", Group: "networking.k8s.io", Version: "v1", Namespace: namespace,
	})
	if !ingressesRead {
		_, _ = fmt.Fprintf(progress,
			"Could not read the namespace's ingresses to check that %s routes to a Service (%s); its backend is unverified.\n",
			host, ingressReadProblem)
		return ""
	}
	backends, hostFound := shipIngressBackendsForHost(ingresses, host)
	if !hostFound {
		_, _ = fmt.Fprintf(progress,
			"No Ingress in namespace %q carries host %s, so ship could not check which Service it routes to.\n",
			namespace, host)
		return ""
	}
	if len(backends) == 0 {
		_, _ = fmt.Fprintf(progress,
			"The Ingress rules for %s name no Service backend, so ship could not check where it routes.\n", host)
		return ""
	}

	services, serviceReadProblem, servicesRead := readShipNamespaceKind(clusterID, client.ResourceRequestItem{
		Kind: "Service", Version: "v1", Namespace: namespace,
	})
	if !servicesRead {
		_, _ = fmt.Fprintf(progress,
			"Could not read the namespace's services to check the Ingress backend of %s (%s); it is unverified.\n",
			host, serviceReadProblem)
		return ""
	}
	servicesByName := shipServicesByName(services)
	existingNames := make([]string, 0, len(servicesByName))
	for name := range servicesByName {
		existingNames = append(existingNames, name)
	}
	sort.Strings(existingNames)
	existing := "there are none"
	if len(existingNames) > 0 {
		existing = "the namespace has " + strings.Join(existingNames, ", ")
	}

	problems := []string{}
	checked := map[string]bool{}
	for _, backend := range backends {
		key := backend.ingressName + "\x00" + backend.serviceName + "\x00" + backend.portNumber + "\x00" + backend.portName
		if checked[key] {
			continue
		}
		checked[key] = true
		service, serviceExists := servicesByName[backend.serviceName]
		if !serviceExists {
			problems = append(problems, fmt.Sprintf(
				"Ingress %q sends it to Service %q, which does not exist in namespace %q (%s)",
				backend.ingressName, backend.serviceName, namespace, existing))
			continue
		}
		if !shipServiceExposesPort(service, backend) {
			problems = append(problems, fmt.Sprintf(
				"Ingress %q sends it to %s of Service %q, which the Service does not expose",
				backend.ingressName, shipBackendPort(backend), backend.serviceName))
			continue
		}
		if strings.EqualFold(service.serviceType, "ExternalName") {
			continue
		}
		endpointRequest := client.ResourceRequestItem{
			Kind: "EndpointSlice", Group: "discovery.k8s.io", Version: "v1", Namespace: namespace,
			LabelSelector: "kubernetes.io/service-name=" + backend.serviceName,
		}
		slices, sliceReadProblem, slicesRead := readShipNamespaceKind(clusterID, endpointRequest)
		for waited := time.Duration(0); slicesRead && shipReadyEndpointCount(slices) == 0 &&
			waited < shipEndpointSettleWindow; waited += shipEndpointSettleInterval {
			if !shipEndpointSettleSleep(waitContext, shipEndpointSettleInterval) {
				break
			}
			slices, sliceReadProblem, slicesRead = readShipNamespaceKind(clusterID, endpointRequest)
		}
		if !slicesRead {
			_, _ = fmt.Fprintf(progress,
				"Could not read the endpoints of Service %q (%s); whether it has a ready pod behind it is unverified.\n",
				backend.serviceName, sliceReadProblem)
			continue
		}
		if shipReadyEndpointCount(slices) == 0 {
			problems = append(problems, fmt.Sprintf(
				"Ingress %q sends it to Service %q, which has no ready endpoints - its selector matches no ready pod",
				backend.ingressName, backend.serviceName))
		}
	}
	return strings.Join(problems, "; ")
}

// unroutableURLError is ship's ending for a deployment whose pods run but
// whose published URL cannot reach them: ship's goal is a live deployment,
// and printing the URL as Live would send the caller to a page that fails.
func unroutableURLError(liveURL string, problem string, namespace string) error {
	return fmt.Errorf(
		"the workload is running, but %s does not route to it: %s - inspect them with "+
			"'ankra cluster get ingresses -n %s' and 'ankra cluster get services -n %s'",
		liveURL, problem, namespace, namespace)
}
