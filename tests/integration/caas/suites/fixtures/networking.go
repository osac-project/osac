// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fulfillmentit "github.com/osac-project/osac/fulfillment-service/it"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

const (
	networkNamespace = "osac-networking"
	networkPlaybook  = "osac-aap/playbook_osac_network_task.yml"
	profilePoolName  = "caas-it-network-profile-pool"
	profilePoolCIDR  = "198.51.100.0/28"
)

type cudnStatusResource struct {
	Metadata metav1.ObjectMeta `json:"metadata"`
	Status   struct {
		Conditions []metav1.Condition `json:"conditions"`
	} `json:"status"`
}

type cudnStatusList struct {
	Items []cudnStatusResource `json:"items"`
}

// ExpectNetworkClassReady waits for the installed manager registrations to resolve.
func ExpectNetworkClassReady(
	ctx SpecContext,
	environment *harness.Environment,
	executions *awx.Installation,
) *osacv1.NetworkClass {
	GinkgoHelper()

	var selected *osacv1.NetworkClass

	Eventually(func(g Gomega) {
		class, err := findNetworkClass(ctx, environment)
		g.Expect(err).NotTo(HaveOccurred())

		if err != nil {
			return
		}

		selected = class.DeepCopy()
		expectProfileManagers(g, class, executions)
		ready, message := networkClassReadiness(class)
		g.Expect(ready).To(BeTrue(), "%s", message)

		if executions.NetworkingEnabled {
			g.Expect(class.Status.NetworkManagers).NotTo(BeNil(), "the managers must be resolved")
		}
	}).WithContext(ctx).WithPolling(time.Second).WithTimeout(2*time.Minute).
		Should(Succeed(), "NetworkClass must be ready before tenant networking starts")

	if executions.NetworkingEnabled && selected != nil {
		expectResolvedNetworkManagers(selected)
	}

	reportNetworkClass(selected, executions)

	return selected
}

// findNetworkClass reads the single NetworkClass owned by the CaaS installation.
func findNetworkClass(ctx SpecContext, environment *harness.Environment) (*osacv1.NetworkClass, error) {
	var classes osacv1.NetworkClassList

	if err := environment.Client.List(ctx, &classes, client.InNamespace(networkNamespace)); err != nil {
		return nil, err
	}

	if len(classes.Items) != 1 {
		return nil, fmt.Errorf("expected exactly one NetworkClass, found %d", len(classes.Items))
	}

	return &classes.Items[0], nil
}

// expectProfileManagers checks that NetworkClass selects the managers named by a profile.
func expectProfileManagers(g Gomega, class *osacv1.NetworkClass, executions *awx.Installation) {
	if executions.ProfileName == "custom" {
		return
	}

	g.Expect(class.Spec.FabricManager).To(Equal(executions.ExpectedFabric),
		"NetworkClass fabric manager must match profile %s", executions.ProfileName)
	g.Expect(class.Spec.K8sManager).To(Equal(executions.ExpectedK8s),
		"NetworkClass Kubernetes manager must match profile %s", executions.ProfileName)
}

// networkClassReadiness returns the Ready condition and useful polling detail.
func networkClassReadiness(class *osacv1.NetworkClass) (bool, string) {
	for _, condition := range class.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == metav1.ConditionTrue,
				fmt.Sprintf("NetworkClass is %s: %s", condition.Reason, condition.Message)
		}
	}

	return false, "NetworkClass has no Ready condition"
}

// expectResolvedNetworkManagers checks resolved registrations against the class spec.
func expectResolvedNetworkManagers(class *osacv1.NetworkClass) {
	managers := class.Status.NetworkManagers
	Expect(managers).NotTo(BeNil())
	Expect(managers.Fabric).NotTo(BeNil())
	Expect(managers.Fabric.Name).To(Equal(class.Spec.FabricManager))

	if class.Spec.K8sManager == "" {
		Expect(managers.Kubernetes).To(BeNil())
		return
	}

	Expect(managers.Kubernetes).NotTo(BeNil())
	Expect(managers.Kubernetes.Name).To(Equal(class.Spec.K8sManager))
}

// reportNetworkClass records profile and manager choices in the test report.
func reportNetworkClass(class *osacv1.NetworkClass, executions *awx.Installation) {
	if executions.NetworkingEnabled && class != nil && class.Status.NetworkManagers != nil {
		resolved := map[string]string{}
		if manager := class.Status.NetworkManagers.Fabric; manager != nil {
			resolved["fabric"] = manager.Name
		}

		if manager := class.Status.NetworkManagers.Kubernetes; manager != nil {
			resolved["kubernetes"] = manager.Name
		}

		AddReportEntry("Resolved networking managers", resolved)
	}

	if executions.ProfileName != "custom" {
		AddReportEntry("Selected networking profile", map[string]string{
			"name":          executions.ProfileName,
			"services":      executions.Services,
			"fabricManager": executions.ExpectedFabric,
			"k8sManager":    executions.ExpectedK8s,
		})
	}
}

// ExpectServiceControllers confirms the profile enabled the real OSAC controllers it selected.
func ExpectServiceControllers(ctx SpecContext, environment *harness.Environment, executions *awx.Installation) {
	GinkgoHelper()

	if executions.ProfileName == "custom" {
		return
	}

	services := profileServices(executions.Services)

	Expect(services).NotTo(BeEmpty(), "profile %s must identify its enabled services", executions.ProfileName)

	var (
		deploymentNames     []string
		observedControllers map[string]string
		operatorAvailable   bool
	)

	Eventually(func(g Gomega) {
		deployments, err := environment.Kube.AppsV1().Deployments(environment.Namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())

		if err != nil {
			return
		}

		deploymentNames, observedControllers, operatorAvailable = findOperatorSettings(deployments.Items)

		g.Expect(observedControllers).NotTo(BeNil(), "the OSAC operator deployment must expose controller settings")
		g.Expect(operatorAvailable).To(BeTrue(), "the OSAC operator deployment must be available")

		if observedControllers == nil {
			return
		}

		expectProfileControllerSettings(g, observedControllers, services, executions)
		g.Expect(bmaasDeploymentAvailable(deployments.Items)).To(Equal(services["bmaas"]),
			"the BMaaS operator availability must match profile %s", executions.ProfileName)
	}).WithContext(ctx).WithPolling(time.Second).WithTimeout(2*time.Minute).
		Should(Succeed(), "required service controllers must match the selected networking profile")

	AddReportEntry("Selected service controllers", map[string]any{
		"profile":          executions.ProfileName,
		"services":         executions.Services,
		"operatorSettings": observedControllers,
		"deployments":      deploymentNames,
	})
}

// profileServices parses the service names enabled by one networking profile.
func profileServices(value string) map[string]bool {
	services := map[string]bool{}

	for service := range strings.SplitSeq(value, ",") {
		if service != "" {
			services[service] = true
		}
	}

	return services
}

// findOperatorSettings collects deployment names and the OSAC controller environment.
func findOperatorSettings(deployments []appsv1.Deployment) ([]string, map[string]string, bool) {
	names := make([]string, 0, len(deployments))

	var observed map[string]string

	operatorAvailable := false

	for index := range deployments {
		deployment := &deployments[index]
		names = append(names, deployment.Name)

		if observed != nil {
			continue
		}

		for _, container := range deployment.Spec.Template.Spec.Containers {
			settings := controllerSettings(container.Env)
			if _, found := settings["OSAC_ENABLE_CLUSTER_CONTROLLER"]; found {
				observed = settings
				operatorAvailable = deployment.Status.AvailableReplicas > 0
				continue
			}
		}
	}

	return names, observed, operatorAvailable
}

// controllerSettings selects the OSAC controller environment variables.
func controllerSettings(environment []corev1.EnvVar) map[string]string {
	settings := map[string]string{}

	for _, variable := range environment {
		if strings.HasPrefix(variable.Name, "OSAC_ENABLE_") || variable.Name == "OSAC_PROVISIONING_ENABLED" {
			settings[variable.Name] = variable.Value
		}
	}

	return settings
}

// expectProfileControllerSettings checks observed service switches against the profile.
func expectProfileControllerSettings(
	g Gomega,
	observed map[string]string,
	services map[string]bool,
	executions *awx.Installation,
) {
	for variable, enabled := range map[string]bool{
		"OSAC_ENABLE_CLUSTER_CONTROLLER":            services["caas"],
		"OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER": services["bmaas"],
		"OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER":   services["vmaas"],
		"OSAC_ENABLE_NETWORKING_CONTROLLER":         true,
		"OSAC_PROVISIONING_ENABLED":                 executions.NetworkingEnabled,
	} {
		g.Expect(observed[variable]).To(Equal(strconv.FormatBool(enabled)),
			"profile %s must configure %s for service selection %s",
			executions.ProfileName, variable, executions.Services)
	}
}

// bmaasDeploymentAvailable reports whether the BMaaS controller deployment is ready.
func bmaasDeploymentAvailable(deployments []appsv1.Deployment) bool {
	for index := range deployments {
		deployment := &deployments[index]
		if strings.Contains(deployment.Name, "bare-metal-fulfillment-operator") {
			return deployment.Spec.Replicas != nil && *deployment.Spec.Replicas > 0 &&
				deployment.Status.AvailableReplicas > 0
		}
	}

	return false
}

// EnsureProfileExternalIPPool creates the provider pool needed by tenant NAT defaults.
func EnsureProfileExternalIPPool(
	ctx SpecContext,
	environment *harness.Environment,
	tool *fulfillmentit.Tool,
	executions *awx.Installation,
	pods *awx.JobPodObserver,
) {
	GinkgoHelper()

	if !executions.NetworkingEnabled {
		return
	}

	pools := privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
	filter := fmt.Sprintf("this.metadata.name == %q && !has(this.metadata.deletion_timestamp)", profilePoolName)
	response, err := pools.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{Filter: &filter}.Build())
	Expect(err).NotTo(HaveOccurred())

	var pool *privatev1.ExternalIPPool

	switch len(response.GetItems()) {
	case 0:
		created, createErr := pools.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{Name: profilePoolName}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{profilePoolCIDR},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(createErr).NotTo(HaveOccurred())

		pool = created.GetObject()
	case 1:
		pool = response.GetItems()[0]
	default:
		Fail("the networking profile must have exactly one external IP pool")
	}

	Expect(pool.GetSpec().GetCidrs()).To(Equal([]string{profilePoolCIDR}))
	Expect(pool.GetSpec().GetIpFamily()).To(Equal(privatev1.IPFamily_IP_FAMILY_IPV4))

	var resources osacv1.ExternalIPPoolList

	Eventually(func(g Gomega) {
		g.Expect(environment.Client.List(ctx, &resources, client.InNamespace(networkNamespace), client.MatchingLabels{
			"osac.openshift.io/externalippool-uuid": pool.GetId(),
		})).To(Succeed())
		g.Expect(resources.Items).To(HaveLen(1), "the ExternalIPPool must be projected into the networking hub")
	}).WithContext(ctx).WithPolling(time.Second).WithTimeout(2*time.Minute).
		Should(Succeed(), "the profile ExternalIPPool must reach the OSAC hub")

	ExpectSuccessfulJobs(ctx, executions, &resources.Items[0], networkPlaybook, pods)

	Eventually(func(g Gomega) {
		observed, getErr := pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: pool.GetId()}.Build())
		g.Expect(getErr).NotTo(HaveOccurred())
		g.Expect(observed.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
		), "the Netris ExternalIPPool task must report ready before tenant defaults start")
	}).WithContext(ctx).WithPolling(time.Second).WithTimeout(2*time.Minute).
		Should(Succeed(), "the profile ExternalIPPool must be ready")

	AddReportEntry("Networking profile ExternalIPPool", map[string]string{
		"name": profilePoolName,
		"id":   pool.GetId(),
		"cidr": profilePoolCIDR,
	})
}

// ExpectTenantNetworkJobs verifies manager selection and job execution for tenant defaults.
func ExpectTenantNetworkJobs(
	ctx SpecContext,
	environment *harness.Environment,
	executions *awx.Installation,
	pods *awx.JobPodObserver,
	tenantID string,
	class *osacv1.NetworkClass,
) {
	GinkgoHelper()

	expectedResources := 3
	if executions.NetworkingEnabled {
		expectedResources = 5
	}

	var resources []osacv1.NetworkResourceObject

	Eventually(func(g Gomega) {
		var err error

		resources, err = defaultNetworkResources(ctx, environment, tenantID)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(resources).To(HaveLen(expectedResources), "tenant %s must have all default networking resources", tenantID)
	}).WithContext(ctx).WithPolling(time.Second).WithTimeout(2*time.Minute).
		Should(Succeed(), "tenant default networking resources must be created")

	for _, resource := range resources {
		if executions.NetworkingEnabled {
			ExpectResourceManagers(resource, class)
		}

		jobs, err := executions.JobsForResource(ctx, resource, networkPlaybook)
		Expect(err).NotTo(HaveOccurred())

		if !executions.NetworkingEnabled {
			Expect(jobs).To(BeEmpty(), "%s must not launch manager jobs in the networking-disabled profile", resource.GetName())
			continue
		}

		ExpectSuccessfulJobs(ctx, executions, resource, networkPlaybook, pods)
	}

	if executions.NetworkingEnabled && executions.ExpectedFabric == "netris" {
		ExpectNetrisFixtureState(ctx)
	}
}

// ExpectNetrisFixtureState confirms successful AWX jobs changed the isolated fake controller.
func ExpectNetrisFixtureState(ctx SpecContext) {
	GinkgoHelper()

	adminURL := os.Getenv("NETRIS_FAKE_ADMIN_URL")
	Expect(adminURL).NotTo(BeEmpty(), "the Netris profile must start its per-run fake fixture")

	stateBody, err := getNetrisFixtureAdmin(ctx, adminURL, "/__admin/state")
	Expect(err).NotTo(HaveOccurred(), "read the Netris fixture's recorded state")

	var state map[string]json.RawMessage

	Expect(json.Unmarshal(stateBody, &state)).To(Succeed())

	counts := map[string]int{}

	for _, collection := range []string{"vpcs", "allocations", "ipam_subnets", "vnets", "nats"} {
		var objects []json.RawMessage

		Expect(json.Unmarshal(state[collection], &objects)).To(Succeed(), "fixture state must include %s", collection)
		Expect(objects).NotTo(BeEmpty(), "production manager jobs must create %s in the fixture", collection)
		counts[collection] = len(objects)
	}

	AddReportEntry("Netris fixture resource counts", counts)

	callsBody, err := getNetrisFixtureAdmin(ctx, adminURL, "/__admin/calls")
	Expect(err).NotTo(HaveOccurred(), "read Netris fixture operation history")

	var calls [][]json.RawMessage

	Expect(json.Unmarshal(callsBody, &calls)).To(Succeed())

	observedCalls := map[string]bool{}

	for _, call := range calls {
		if len(call) < 2 {
			continue
		}

		var method, path string

		if json.Unmarshal(call[0], &method) == nil && json.Unmarshal(call[1], &path) == nil {
			observedCalls[method+" "+path] = true
		}
	}

	for _, operation := range []string{
		"POST /api/v2/vpc",
		"POST /api/v2/ipam/allocation",
		"POST /api/v2/ipam/subnet",
		"POST /api/v2/vnet",
		"POST /api/v2/nat",
	} {
		Expect(observedCalls).To(HaveKey(operation), "production Netris jobs must issue %s", operation)
	}

	AddReportEntry("Netris fixture operations", []string{
		"POST /api/v2/vpc",
		"POST /api/v2/ipam/allocation",
		"POST /api/v2/ipam/subnet",
		"POST /api/v2/vnet",
		"POST /api/v2/nat",
	})
}

// getNetrisFixtureAdmin calls one of the loopback-only fake-controller endpoints.
func getNetrisFixtureAdmin(ctx context.Context, rawURL, path string) ([]byte, error) {
	target, err := localNetrisAdminURL(rawURL, path)
	if err != nil {
		return nil, err
	}

	// #nosec G704 -- localNetrisAdminURL restricts the request to loopback.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}

	// #nosec G704 -- localNetrisAdminURL restricts this request to the local port forward.
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("netris fixture admin request returned HTTP %d", response.StatusCode)
	}

	return io.ReadAll(response.Body)
}

// localNetrisAdminURL accepts only loopback port-forward URLs and fixed admin paths.
func localNetrisAdminURL(rawURL, path string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Netris fixture admin URL: %w", err)
	}

	if parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") ||
		(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		return "", fmt.Errorf("netris fixture admin URL must use a loopback-only port forward")
	}

	if path != "/__admin/state" && path != "/__admin/calls" {
		return "", fmt.Errorf("unsupported Netris fixture admin path")
	}

	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("netris fixture admin URL must include a valid port")
	}

	return (&url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), Path: path}).String(), nil
}

// ExpectCUDNManagerReady verifies the production Kubernetes Manager created CUDNs accepted by the OVN fixture.
func ExpectCUDNManagerReady(ctx SpecContext, environment *harness.Environment) {
	GinkgoHelper()

	var list cudnStatusList

	var observed int

	Eventually(func(g Gomega) {
		body, err := environment.Kube.CoreV1().RESTClient().Get().
			AbsPath("/apis/k8s.ovn.org/v1/clusteruserdefinednetworks").
			Do(ctx).Raw()
		g.Expect(err).NotTo(HaveOccurred())

		if err != nil {
			return
		}

		g.Expect(json.Unmarshal(body, &list)).To(Succeed())

		selected := make([]cudnStatusResource, 0, len(list.Items))
		for _, cudn := range list.Items {
			if cudn.Metadata.Labels["osac.openshift.io/k8s-manager"] == "cudn_evpn" {
				selected = append(selected, cudn)
			}
		}

		g.Expect(selected).NotTo(BeEmpty(), "the production Kubernetes Manager must create a CUDN")

		observed = len(selected)
		for _, cudn := range selected {
			g.Expect(cudn.Status.Conditions).NotTo(BeEmpty(),
				"CUDN %s must receive OVN fixture status", cudn.Metadata.Name)

			ready := map[string]bool{}

			for _, condition := range cudn.Status.Conditions {
				if condition.Status == metav1.ConditionTrue {
					ready[condition.Type] = true
				}
			}

			g.Expect(ready["TransportAccepted"]).To(BeTrue(), "CUDN %s transport must be accepted", cudn.Metadata.Name)
			g.Expect(ready["NetworkCreated"]).To(BeTrue(), "CUDN %s network must be created", cudn.Metadata.Name)
			g.Expect(ready["NetworkAllocationSucceeded"]).To(BeTrue(), "CUDN %s allocation must succeed", cudn.Metadata.Name)
		}
	}).WithContext(ctx).WithPolling(time.Second).WithTimeout(2*time.Minute).
		Should(Succeed(), "production Kubernetes Manager resources must be accepted by the OVN fixture")

	AddReportEntry("OVN fixture CUDN count", observed)
}

// defaultNetworkResources returns this tenant's hub resources for AWX correlation.
func defaultNetworkResources(
	ctx SpecContext,
	environment *harness.Environment,
	tenantID string,
) ([]osacv1.NetworkResourceObject, error) {
	GinkgoHelper()

	resources := make([]osacv1.NetworkResourceObject, 0, 5)

	var virtualNetworks osacv1.VirtualNetworkList

	if err := environment.Client.List(ctx, &virtualNetworks, client.InNamespace(networkNamespace)); err != nil {
		return nil, fmt.Errorf("list tenant VirtualNetworks: %w", err)
	}

	for index := range virtualNetworks.Items {
		resource := &virtualNetworks.Items[index]
		if resource.GetAnnotations()[harness.TenantAnnotation] == tenantID {
			resources = append(resources, resource)
		}
	}

	var subnets osacv1.SubnetList

	if err := environment.Client.List(ctx, &subnets, client.InNamespace(networkNamespace)); err != nil {
		return nil, fmt.Errorf("list tenant Subnets: %w", err)
	}

	for index := range subnets.Items {
		resource := &subnets.Items[index]
		if resource.GetAnnotations()[harness.TenantAnnotation] == tenantID {
			resources = append(resources, resource)
		}
	}

	var securityGroups osacv1.SecurityGroupList

	if err := environment.Client.List(ctx, &securityGroups, client.InNamespace(networkNamespace)); err != nil {
		return nil, fmt.Errorf("list tenant SecurityGroups: %w", err)
	}

	for index := range securityGroups.Items {
		resource := &securityGroups.Items[index]
		if resource.GetAnnotations()[harness.TenantAnnotation] == tenantID {
			resources = append(resources, resource)
		}
	}

	var externalIPs osacv1.ExternalIPList

	if err := environment.Client.List(ctx, &externalIPs, client.InNamespace(networkNamespace)); err != nil {
		return nil, fmt.Errorf("list tenant ExternalIPs: %w", err)
	}

	for index := range externalIPs.Items {
		resource := &externalIPs.Items[index]
		if resource.GetAnnotations()[harness.TenantAnnotation] == tenantID {
			resources = append(resources, resource)
		}
	}

	var natGateways osacv1.NATGatewayList

	if err := environment.Client.List(ctx, &natGateways, client.InNamespace(networkNamespace)); err != nil {
		return nil, fmt.Errorf("list tenant NATGateways: %w", err)
	}

	for index := range natGateways.Items {
		resource := &natGateways.Items[index]
		if resource.GetAnnotations()[harness.TenantAnnotation] == tenantID {
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

// ExpectResourceManagers confirms each tenant resource uses its immutable NetworkClass selection.
func ExpectResourceManagers(resource osacv1.NetworkResourceObject, class *osacv1.NetworkClass) {
	GinkgoHelper()

	managers := resource.GetNetworkManagers()
	Expect(managers).NotTo(BeNil(), "%s must record its selected managers", resource.GetName())
	Expect(managers.Fabric).NotTo(BeNil())
	Expect(managers.Fabric.Name).To(Equal(class.Spec.FabricManager))

	if class.Spec.K8sManager == "" {
		Expect(managers.Kubernetes).To(BeNil())
		return
	}

	Expect(managers.Kubernetes).NotTo(BeNil())
	Expect(managers.Kubernetes.Name).To(Equal(class.Spec.K8sManager))
}
