/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

// Helpers of the networking design specs (unified networking, default networking and the workload networking
// designs). The specs talk to the deployed fulfillment service through its private and public gRPC APIs. In the
// service-only phase of the SUITE=fulfillment target no OSAC operator runs, so the fixtures stand in for its feedback
// controllers and write networking status through the private API; the specs labelled networkingOperatorLabel run
// against the deployed operator and never write status themselves.

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	appsv1 "k8s.io/api/apps/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// networkingOperatorLabel marks the specs that need the deployed OSAC operator. The SUITE=fulfillment installer target
// runs them in its own phase, after it deploys the operator with global.networking.provisioningEnabled=false, and
// excludes them from the service-only phase.
const networkingOperatorLabel = "networking-operator"

// ndHoldFinalizer is a test finalizer that keeps a record whose delete was accepted, so a spec can inspect the
// DELETING record before the fulfillment controller removes it.
const ndHoldFinalizer = "osac.openshift.io/it-networking-hold"

// ndDefaultLabel is the label of tenant default networking resources.
const ndDefaultLabel = "osac.openshift.io/default"

// ndTimeout is the timeout of the Eventually checks of the networking design specs.
const ndTimeout = 2 * time.Minute

// ndPoll is the polling interval of the Eventually checks of the networking design specs.
const ndPoll = 500 * time.Millisecond

// ndClients holds the clients of the networking design specs: private clients with the administrator identity and
// public clients with the identity of the regular test user, which belongs to the tenant usersGroup.
type ndClients struct {
	capabilities    privatev1.CapabilitiesClient
	tenants         privatev1.TenantsClient
	projects        privatev1.ProjectsClient
	networkClasses  privatev1.NetworkClassesClient
	virtualNetworks privatev1.VirtualNetworksClient
	subnets         privatev1.SubnetsClient
	securityGroups  privatev1.SecurityGroupsClient
	pools           privatev1.ExternalIPPoolsClient
	externalIPs     privatev1.ExternalIPsClient
	attachments     privatev1.ExternalIPAttachmentsClient
	natGateways     privatev1.NATGatewaysClient

	publicVirtualNetworks publicv1.VirtualNetworksClient
	publicSubnets         publicv1.SubnetsClient
	publicSecurityGroups  publicv1.SecurityGroupsClient
	publicPools           publicv1.ExternalIPPoolsClient
	publicExternalIPs     publicv1.ExternalIPsClient
	publicAttachments     publicv1.ExternalIPAttachmentsClient
	publicNATGateways     publicv1.NATGatewaysClient
}

func newNDClients() ndClients {
	adminConn := tool.InternalView().AdminConn()
	userConn := tool.ExternalView().UserConn()
	return ndClients{
		capabilities:    privatev1.NewCapabilitiesClient(adminConn),
		tenants:         privatev1.NewTenantsClient(adminConn),
		projects:        privatev1.NewProjectsClient(adminConn),
		networkClasses:  privatev1.NewNetworkClassesClient(adminConn),
		virtualNetworks: privatev1.NewVirtualNetworksClient(adminConn),
		subnets:         privatev1.NewSubnetsClient(adminConn),
		securityGroups:  privatev1.NewSecurityGroupsClient(adminConn),
		pools:           privatev1.NewExternalIPPoolsClient(adminConn),
		externalIPs:     privatev1.NewExternalIPsClient(adminConn),
		attachments:     privatev1.NewExternalIPAttachmentsClient(adminConn),
		natGateways:     privatev1.NewNATGatewaysClient(adminConn),

		publicVirtualNetworks: publicv1.NewVirtualNetworksClient(userConn),
		publicSubnets:         publicv1.NewSubnetsClient(userConn),
		publicSecurityGroups:  publicv1.NewSecurityGroupsClient(userConn),
		publicPools:           publicv1.NewExternalIPPoolsClient(userConn),
		publicExternalIPs:     publicv1.NewExternalIPsClient(userConn),
		publicAttachments:     publicv1.NewExternalIPAttachmentsClient(userConn),
		publicNATGateways:     publicv1.NewNATGatewaysClient(userConn),
	}
}

// ndLog writes an object to the Ginkgo output, so a failing spec shows the state that it checked.
func ndLog(title string, object proto.Message) {
	if object == nil {
		fmt.Fprintf(GinkgoWriter, "%s: <nil>\n", title)
		return
	}
	text, err := protojson.MarshalOptions{Multiline: true, EmitUnpopulated: false}.Marshal(object)
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "%s: <cannot marshal: %v>\n", title, err)
		return
	}
	fmt.Fprintf(GinkgoWriter, "%s:\n%s\n", title, text)
}

// ndText renders an object for failure messages.
func ndText(object proto.Message) string {
	if object == nil {
		return "<nil>"
	}
	text, err := protojson.Marshal(object)
	if err != nil {
		return fmt.Sprintf("<cannot marshal: %v>", err)
	}
	return string(text)
}

// ndExpectCode checks that an error has the gRPC code and that its message contains every fragment.
func ndExpectCode(err error, code grpccodes.Code, fragments ...string) {
	GinkgoHelper()
	Expect(err).To(HaveOccurred(), "expected a %s error, but the request succeeded", code)
	status := grpcstatus.Convert(err)
	fmt.Fprintf(GinkgoWriter, "Got the expected error: code=%s message=%q\n", status.Code(), status.Message())
	Expect(status.Code()).To(Equal(code), "unexpected gRPC code; message: %q", status.Message())
	for _, fragment := range fragments {
		Expect(status.Message()).To(ContainSubstring(fragment), "the error message doesn't name %q", fragment)
	}
}

// ndProviderNetworkingEnabled returns the global.networking.provisioningEnabled value that the deployed fulfillment
// service reports in its private capabilities.
func ndProviderNetworkingEnabled(ctx context.Context) bool {
	GinkgoHelper()
	response, err := newNDClients().capabilities.Get(ctx, privatev1.CapabilitiesGetRequest_builder{}.Build())
	Expect(err).ToNot(HaveOccurred())
	return response.GetNetworkingProvisioningEnabled()
}

// ndSkipUnlessProviderNetworkingEnabled skips a spec that needs provider networking enabled, for example because it
// creates an ExternalIP. The SUITE=fulfillment target deploys with provider networking disabled; the disabled
// behaviour is covered by the networking design specs.
func ndSkipUnlessProviderNetworkingEnabled(ctx context.Context, what string) {
	if !ndProviderNetworkingEnabled(ctx) {
		Skip(fmt.Sprintf("%s needs global.networking.provisioningEnabled=true; this deployment has provider "+
			"networking disabled, where an ExternalIP create fails with FAILED_PRECONDITION", what))
	}
}

// ndSkipIfProviderNetworkingEnabled skips a spec that checks the provider-networking-disabled behaviour.
func ndSkipIfProviderNetworkingEnabled(ctx context.Context, what string) {
	if ndProviderNetworkingEnabled(ctx) {
		Skip(fmt.Sprintf("%s checks global.networking.provisioningEnabled=false; this deployment has it enabled", what))
	}
}

// ndUniqueName returns a unique, DNS-compatible resource name with the given prefix.
func ndUniqueName(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.New()[24:32])
}

// ndVirtualNetworkCIDR returns a /16 for a test VirtualNetwork. VirtualNetworks may overlap each other; the range
// 10.150.0.0/16 to 10.199.0.0/16 overlaps neither the NetworkClass default VirtualNetwork (10.200.0.0/16), nor the
// default Cluster pod and service networks (10.128.0.0/14, 172.30.0.0/16), nor the ExternalIPPools of the suite
// (uniqueCIDR, in 100.64.0.0/10).
func ndVirtualNetworkCIDR() string {
	return fmt.Sprintf("10.%d.0.0/16", 150+rand.IntN(50))
}

// ndPoolBase returns the network address of a unique /28 for an ExternalIPPool, for example "100.64.3.0".
func ndPoolBase() string {
	return strings.TrimSuffix(uniqueCIDR(), "/28")
}

// ndStatusUpdate runs a private status update, retrying version conflicts with the controllers.
func ndStatusUpdate(ctx context.Context, what string, update func() error) {
	GinkgoHelper()
	Eventually(func() error {
		err := update()
		if err != nil && grpcstatus.Code(err) != grpccodes.Aborted {
			return StopTrying(fmt.Sprintf("%s failed", what)).Wrap(err)
		}
		return err
	}, 30*time.Second, 100*time.Millisecond).Should(Succeed(), what)
}

// ndRequireOperatorDeployed fails the spec when the OSAC operator isn't deployed and available: the specs labelled
// networkingOperatorLabel check the operator's behaviour and never stand in for it.
func ndRequireOperatorDeployed(ctx context.Context) {
	GinkgoHelper()
	deployments := &appsv1.DeploymentList{}
	Expect(tool.KubeClient().List(ctx, deployments, crclient.InNamespace(hubNamespace))).To(Succeed())
	for _, deployment := range deployments.Items {
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if strings.Contains(container.Image, "osac-operator") {
				fmt.Fprintf(GinkgoWriter, "Found the OSAC operator deployment %s/%s (image %s, %d available)\n",
					deployment.Namespace, deployment.Name, container.Image, deployment.Status.AvailableReplicas)
				Expect(deployment.Status.AvailableReplicas).To(BeNumerically(">=", 1),
					"the OSAC operator deployment %s/%s has no available replica", deployment.Namespace,
					deployment.Name)
				return
			}
		}
	}
	Fail(fmt.Sprintf("the specs labelled %q need the OSAC operator, but no deployment in namespace %q runs it; "+
		"run them through the SUITE=fulfillment installer target or deploy the operator first",
		networkingOperatorLabel, hubNamespace))
}

// ndSingletonNetworkClass returns the deployment NetworkClass, failing when there isn't exactly one.
func ndSingletonNetworkClass(ctx context.Context, c ndClients) *privatev1.NetworkClass {
	GinkgoHelper()
	response, err := c.networkClasses.List(ctx, privatev1.NetworkClassesListRequest_builder{}.Build())
	Expect(err).ToNot(HaveOccurred())
	Expect(response.GetItems()).To(HaveLen(1), "expected exactly one NetworkClass, got %d", len(response.GetItems()))
	return response.GetItems()[0]
}

// ndSetNetworkClassStatus writes the state and message of the deployment NetworkClass through the private API, as
// the operator's NetworkClass feedback controller does.
func ndSetNetworkClassStatus(ctx context.Context, c ndClients, id string, state privatev1.NetworkClassState,
	message string) {
	GinkgoHelper()
	ndStatusUpdate(ctx, fmt.Sprintf("set NetworkClass %s to %s", id, state), func() error {
		response, err := c.networkClasses.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		if !object.HasStatus() {
			object.SetStatus(&privatev1.NetworkClassStatus{})
		}
		object.GetStatus().SetState(state)
		if message == "" {
			object.GetStatus().ClearMessage()
		} else {
			object.GetStatus().SetMessage(message)
		}
		_, err = c.networkClasses.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.message"}},
			Lock:       true,
		}.Build())
		return err
	})
}

// ndSetSecurityGroupICMP records status.security_group_icmp on the deployment NetworkClass, as the operator's feedback
// controller would. nil clears it. The value is cleared when the spec ends, because every suite shares the singleton.
func ndSetSecurityGroupICMP(ctx context.Context, c ndClients, value *bool) {
	GinkgoHelper()
	write := func(ctx context.Context, value *bool) {
		GinkgoHelper()
		id := ndSingletonNetworkClass(ctx, c).GetId()
		ndStatusUpdate(ctx, fmt.Sprintf("set status.security_group_icmp of NetworkClass %s", id), func() error {
			response, err := c.networkClasses.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			if !object.HasStatus() {
				object.SetStatus(&privatev1.NetworkClassStatus{})
			}
			if value == nil {
				object.GetStatus().ClearSecurityGroupIcmp()
			} else {
				object.GetStatus().SetSecurityGroupIcmp(*value)
			}
			_, err = c.networkClasses.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.security_group_icmp"}},
				Lock:       true,
			}.Build())
			return err
		})
	}
	write(ctx, value)
	DeferCleanup(func(cleanupCtx context.Context) { write(cleanupCtx, nil) })
}

// ndReadyNetworkClass returns the deployment NetworkClass in state READY. In the service-only phase no operator
// resolves it, so the fixture reports it READY as the operator would.
func ndReadyNetworkClass(ctx context.Context, c ndClients) *privatev1.NetworkClass {
	GinkgoHelper()
	response, err := sharedNetworkClass(ctx, c.networkClasses)
	Expect(err).ToNot(HaveOccurred())
	waitForNetworkClassReady(ctx, c.networkClasses, response.GetObject().GetId())
	return ndSingletonNetworkClass(ctx, c)
}

// ndCreateVirtualNetwork creates a VirtualNetwork through the private API and registers its cleanup.
func ndCreateVirtualNetwork(ctx context.Context, c ndClients, tenant, cidr string) *privatev1.VirtualNetwork {
	GinkgoHelper()
	By(fmt.Sprintf("creating VirtualNetwork %s in tenant %s", cidr, tenant))
	response, err := c.virtualNetworks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
		Object: privatev1.VirtualNetwork_builder{
			Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-vn"), Tenant: tenant}.Build(),
			Spec:     privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new(cidr)}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred(), "VirtualNetwork %s create failed", cidr)
	object := response.GetObject()
	ndLog("Created VirtualNetwork", object)
	id := object.GetId()
	DeferCleanup(func(cleanupCtx context.Context) {
		Expect(cleanupVirtualNetworkFixture(cleanupCtx, c.virtualNetworks, id)).To(Succeed())
	})
	return object
}

// ndWaitVirtualNetworkPlaced waits until the fulfillment controller has placed a VirtualNetwork on the hub, so a
// later status write of the fixture doesn't race the controller's own writes.
func ndWaitVirtualNetworkPlaced(ctx context.Context, c ndClients, id string) *privatev1.VirtualNetwork {
	GinkgoHelper()
	var object *privatev1.VirtualNetwork
	Eventually(func(g Gomega) {
		response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		object = response.GetObject()
		g.Expect(object.GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING),
			"VirtualNetwork %s isn't PENDING: %s", id, ndText(object))
		g.Expect(object.GetStatus().GetHub()).ToNot(BeEmpty(), "VirtualNetwork %s has no hub yet: %s", id,
			ndText(object))
	}, ndTimeout, ndPoll).Should(Succeed(), "VirtualNetwork %s was not placed on the hub", id)
	return object
}

// ndSetVirtualNetworkState writes the state of a VirtualNetwork through the private API, as the operator's feedback
// controller does.
func ndSetVirtualNetworkState(ctx context.Context, c ndClients, id string, state privatev1.VirtualNetworkState) {
	GinkgoHelper()
	ndStatusUpdate(ctx, fmt.Sprintf("set VirtualNetwork %s to %s", id, state), func() error {
		response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		if !object.HasStatus() {
			object.SetStatus(&privatev1.VirtualNetworkStatus{})
		}
		object.GetStatus().SetState(state)
		_, err = c.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			Lock:       true,
		}.Build())
		return err
	})
	Eventually(func(g Gomega) {
		response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(state), "VirtualNetwork %s: %s", id,
			ndText(response.GetObject()))
	}, ndTimeout, ndPoll).Should(Succeed())
}

// ndReadyVirtualNetwork creates a VirtualNetwork and reports it READY.
func ndReadyVirtualNetwork(ctx context.Context, c ndClients, tenant, cidr string) *privatev1.VirtualNetwork {
	GinkgoHelper()
	object := ndCreateVirtualNetwork(ctx, c, tenant, cidr)
	ndWaitVirtualNetworkPlaced(ctx, c, object.GetId())
	By(fmt.Sprintf("reporting VirtualNetwork %s READY (no operator in the service-only phase)", object.GetId()))
	ndSetVirtualNetworkState(ctx, c, object.GetId(), privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
	return object
}

// ndCreateSubnet creates a Subnet through the private API and registers its cleanup.
func ndCreateSubnet(ctx context.Context, c ndClients, tenant, virtualNetworkID, cidr string) *privatev1.Subnet {
	GinkgoHelper()
	By(fmt.Sprintf("creating Subnet %s in VirtualNetwork %s", cidr, virtualNetworkID))
	response, err := c.subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{
		Object: privatev1.Subnet_builder{
			Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-subnet"), Tenant: tenant}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
				Ipv4Cidr:       new(cidr),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred(), "Subnet %s create failed", cidr)
	object := response.GetObject()
	ndLog("Created Subnet", object)
	id := object.GetId()
	DeferCleanup(func(cleanupCtx context.Context) {
		Expect(cleanupSubnetFixture(cleanupCtx, c.subnets, id)).To(Succeed())
	})
	return object
}

// ndSetSubnetState waits until the controller placed the Subnet, then writes its state through the private API.
func ndSetSubnetState(ctx context.Context, c ndClients, id string, state privatev1.SubnetState) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		response, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetHub()).ToNot(BeEmpty(), "Subnet %s has no hub yet: %s", id,
			ndText(response.GetObject()))
	}, ndTimeout, ndPoll).Should(Succeed(), "Subnet %s was not placed on the hub", id)
	ndStatusUpdate(ctx, fmt.Sprintf("set Subnet %s to %s", id, state), func() error {
		response, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		if !object.HasStatus() {
			object.SetStatus(&privatev1.SubnetStatus{})
		}
		object.GetStatus().SetState(state)
		_, err = c.subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			Lock:       true,
		}.Build())
		return err
	})
	Eventually(func(g Gomega) {
		response, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(state), "Subnet %s: %s", id,
			ndText(response.GetObject()))
	}, ndTimeout, ndPoll).Should(Succeed())
}

// ndReadySubnet creates a Subnet and reports it READY.
func ndReadySubnet(ctx context.Context, c ndClients, tenant, virtualNetworkID, cidr string) *privatev1.Subnet {
	GinkgoHelper()
	object := ndCreateSubnet(ctx, c, tenant, virtualNetworkID, cidr)
	By(fmt.Sprintf("reporting Subnet %s READY (no operator in the service-only phase)", object.GetId()))
	ndSetSubnetState(ctx, c, object.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
	return object
}

// ndPublicRule builds a public SecurityGroup rule; ports of 0 are left unset.
func ndPublicRule(protocol publicv1.Protocol, cidr string, portFrom, portTo int32) *publicv1.SecurityRule {
	builder := publicv1.SecurityRule_builder{Protocol: protocol, Ipv4Cidr: new(cidr)}
	if portFrom != 0 {
		builder.PortFrom = new(portFrom)
	}
	if portTo != 0 {
		builder.PortTo = new(portTo)
	}
	return builder.Build()
}

// ndCreateSecurityGroup creates a SecurityGroup through the private API and registers its cleanup.
func ndCreateSecurityGroup(ctx context.Context, c ndClients, tenant, virtualNetworkID string,
	ingress, egress []*privatev1.SecurityRule) *privatev1.SecurityGroup {
	GinkgoHelper()
	By(fmt.Sprintf("creating a SecurityGroup in VirtualNetwork %s", virtualNetworkID))
	response, err := c.securityGroups.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
		Object: privatev1.SecurityGroup_builder{
			Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-sg"), Tenant: tenant}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
				Ingress:        ingress,
				Egress:         egress,
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred(), "SecurityGroup create failed")
	object := response.GetObject()
	ndLog("Created SecurityGroup", object)
	id := object.GetId()
	DeferCleanup(func(cleanupCtx context.Context) {
		Expect(cleanupSecurityGroupFixture(cleanupCtx, c.securityGroups, id)).To(Succeed())
	})
	return object
}

// ndSetSecurityGroupStatus waits until the controller placed the SecurityGroup, then writes its state and message
// through the private API, as the operator's feedback controller does.
func ndSetSecurityGroupStatus(ctx context.Context, c ndClients, id string, state privatev1.SecurityGroupState,
	message string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetHub()).ToNot(BeEmpty(), "SecurityGroup %s has no hub yet: %s",
			id, ndText(response.GetObject()))
	}, ndTimeout, ndPoll).Should(Succeed(), "SecurityGroup %s was not placed on the hub", id)
	ndStatusUpdate(ctx, fmt.Sprintf("set SecurityGroup %s to %s", id, state), func() error {
		response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		if !object.HasStatus() {
			object.SetStatus(&privatev1.SecurityGroupStatus{})
		}
		object.GetStatus().SetState(state)
		if message == "" {
			object.GetStatus().ClearMessage()
		} else {
			object.GetStatus().SetMessage(message)
		}
		_, err = c.securityGroups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.message"}},
			Lock:       true,
		}.Build())
		return err
	})
	Eventually(func(g Gomega) {
		response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(state), "SecurityGroup %s: %s", id,
			ndText(response.GetObject()))
		g.Expect(response.GetObject().GetStatus().GetMessage()).To(Equal(message), "SecurityGroup %s: %s", id,
			ndText(response.GetObject()))
	}, ndTimeout, ndPoll).Should(Succeed())
}

// ndReadySecurityGroup creates a SecurityGroup and reports it READY.
func ndReadySecurityGroup(ctx context.Context, c ndClients, tenant, virtualNetworkID string,
	ingress, egress []*privatev1.SecurityRule) *privatev1.SecurityGroup {
	GinkgoHelper()
	object := ndCreateSecurityGroup(ctx, c, tenant, virtualNetworkID, ingress, egress)
	By(fmt.Sprintf("reporting SecurityGroup %s READY (no operator in the service-only phase)", object.GetId()))
	ndSetSecurityGroupStatus(ctx, c, object.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, "")
	return object
}

// ndCreatePool creates an ExternalIPPool through the private API and registers its cleanup.
func ndCreatePool(ctx context.Context, c ndClients, cidr string) *privatev1.ExternalIPPool {
	GinkgoHelper()
	By(fmt.Sprintf("creating ExternalIPPool %s", cidr))
	response, err := c.pools.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
		Object: privatev1.ExternalIPPool_builder{
			Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-pool")}.Build(),
			Spec: privatev1.ExternalIPPoolSpec_builder{
				Cidrs:    []string{cidr},
				IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred(), "ExternalIPPool %s create failed", cidr)
	object := response.GetObject()
	ndLog("Created ExternalIPPool", object)
	id := object.GetId()
	DeferCleanup(func(cleanupCtx context.Context) {
		Expect(cleanupExternalIPPoolFixture(cleanupCtx, c.pools, id)).To(Succeed())
	})
	return object
}

// ndSetPoolState waits until the controller placed the pool, then writes its state through the private API.
func ndSetPoolState(ctx context.Context, c ndClients, id string, state privatev1.ExternalIPPoolState) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		response, err := c.pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetHub()).ToNot(BeEmpty(), "ExternalIPPool %s has no hub yet: %s",
			id, ndText(response.GetObject()))
	}, ndTimeout, ndPoll).Should(Succeed(), "ExternalIPPool %s was not placed on the hub", id)
	ndStatusUpdate(ctx, fmt.Sprintf("set ExternalIPPool %s to %s", id, state), func() error {
		response, err := c.pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		object.GetStatus().SetState(state)
		_, err = c.pools.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			Lock:       true,
		}.Build())
		return err
	})
}

// ndMutateVirtualNetworkFinalizers changes the finalizers of a VirtualNetwork record through the private API.
func ndMutateVirtualNetworkFinalizers(ctx context.Context, c ndClients, id string, mutate func([]string) []string) {
	GinkgoHelper()
	ndStatusUpdate(ctx, fmt.Sprintf("change the finalizers of VirtualNetwork %s", id), func() error {
		response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		object.GetMetadata().SetFinalizers(mutate(slices.Clone(object.GetMetadata().GetFinalizers())))
		_, err = c.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		return err
	})
}

// ndMutateSubnetFinalizers changes the finalizers of a Subnet record through the private API.
func ndMutateSubnetFinalizers(ctx context.Context, c ndClients, id string, mutate func([]string) []string) {
	GinkgoHelper()
	ndStatusUpdate(ctx, fmt.Sprintf("change the finalizers of Subnet %s", id), func() error {
		response, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		object.GetMetadata().SetFinalizers(mutate(slices.Clone(object.GetMetadata().GetFinalizers())))
		_, err = c.subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		return err
	})
}

// ndMutateSecurityGroupFinalizers changes the finalizers of a SecurityGroup record through the private API.
func ndMutateSecurityGroupFinalizers(ctx context.Context, c ndClients, id string, mutate func([]string) []string) {
	GinkgoHelper()
	ndStatusUpdate(ctx, fmt.Sprintf("change the finalizers of SecurityGroup %s", id), func() error {
		response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		object.GetMetadata().SetFinalizers(mutate(slices.Clone(object.GetMetadata().GetFinalizers())))
		_, err = c.securityGroups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		return err
	})
}

func ndRemoveVirtualNetworkHoldIfPresent(ctx context.Context, c ndClients, id string) {
	ndRemoveHoldIfPresent(ctx, fmt.Sprintf("release the test finalizer of VirtualNetwork %s", id), func(ctx context.Context) error {
		response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		object.GetMetadata().SetFinalizers(ndRemoveHold(slices.Clone(object.GetMetadata().GetFinalizers())))
		_, err = c.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		return err
	})
}

func ndRemoveSubnetHoldIfPresent(ctx context.Context, c ndClients, id string) {
	ndRemoveHoldIfPresent(ctx, fmt.Sprintf("release the test finalizer of Subnet %s", id), func(ctx context.Context) error {
		response, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		object.GetMetadata().SetFinalizers(ndRemoveHold(slices.Clone(object.GetMetadata().GetFinalizers())))
		_, err = c.subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		return err
	})
}

func ndRemoveSecurityGroupHoldIfPresent(ctx context.Context, c ndClients, id string) {
	ndRemoveHoldIfPresent(ctx, fmt.Sprintf("release the test finalizer of SecurityGroup %s", id), func(ctx context.Context) error {
		response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		object := response.GetObject()
		object.GetMetadata().SetFinalizers(ndRemoveHold(slices.Clone(object.GetMetadata().GetFinalizers())))
		_, err = c.securityGroups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		return err
	})
}

// ndRemoveHoldIfPresent releases the hold and treats a record that is already gone as success. Specs call it inside
// the polled wait for the delete, not once before it: the controllers write finalizers without a version check, so a
// stale reconcile can overwrite the release and restore the hold, and nothing else would release it again.
func ndRemoveHoldIfPresent(ctx context.Context, what string, update func(context.Context) error) {
	GinkgoHelper()
	ndStatusUpdate(ctx, what, func() error {
		err := update(ctx)
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return nil
		}
		return err
	})
}

// ndAddHold returns a finalizer mutation that adds ndHoldFinalizer.
func ndAddHold(finalizers []string) []string {
	if slices.Contains(finalizers, ndHoldFinalizer) {
		return finalizers
	}
	return append(finalizers, ndHoldFinalizer)
}

// ndRemoveHold returns a finalizer mutation that removes ndHoldFinalizer.
func ndRemoveHold(finalizers []string) []string {
	return slices.DeleteFunc(finalizers, func(value string) bool { return value == ndHoldFinalizer })
}

// ndCreateTenant creates a tenant, waits until the tenant controller synced it and registers its deletion.
func ndCreateTenant(ctx context.Context, c ndClients) (id, name string) {
	GinkgoHelper()
	name = ndUniqueName("nd-tenant")
	By(fmt.Sprintf("creating tenant %s", name))
	id = createTenant(ctx, c.tenants, name)
	DeferCleanup(func(cleanupCtx context.Context) {
		deleteTenant(cleanupCtx, c.tenants, c.projects, id, name)
	})
	waitForTenantSynced(ctx, c.tenants, id)
	return id, name
}

// ndDefaultNetworkingCondition returns the DefaultNetworkingReady condition of a tenant, or nil.
func ndDefaultNetworkingCondition(ctx context.Context, c ndClients, tenantID string) (*privatev1.TenantCondition,
	*privatev1.Tenant, error) {
	response, err := c.tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
	if err != nil {
		return nil, nil, err
	}
	tenant := response.GetObject()
	return findTenantCondition(tenant.GetStatus().GetConditions(),
		privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY), tenant, nil
}

// ndExpectDefaultNetworkingCondition waits until the DefaultNetworkingReady condition of a tenant has the status and
// reason, and its message contains every fragment.
func ndExpectDefaultNetworkingCondition(ctx context.Context, c ndClients, tenantID string,
	status privatev1.ConditionStatus, reason string, fragments ...string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		condition, tenant, err := ndDefaultNetworkingCondition(ctx, c, tenantID)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(condition).ToNot(BeNil(), "tenant %s has no DefaultNetworkingReady condition: %s", tenantID,
			ndText(tenant))
		g.Expect(condition.GetStatus()).To(Equal(status), "tenant %s condition: %s", tenantID, ndText(condition))
		g.Expect(condition.GetReason()).To(Equal(reason), "tenant %s condition: %s", tenantID, ndText(condition))
		for _, fragment := range fragments {
			g.Expect(condition.GetMessage()).To(ContainSubstring(fragment), "tenant %s condition: %s", tenantID,
				ndText(condition))
		}
	}, ndTimeout, ndPoll).Should(Succeed(), "tenant %s didn't report DefaultNetworkingReady=%s/%s", tenantID,
		status, reason)
	condition, _, err := ndDefaultNetworkingCondition(ctx, c, tenantID)
	Expect(err).ToNot(HaveOccurred())
	ndLog(fmt.Sprintf("DefaultNetworkingReady of tenant %s", tenantID), condition)
}

// ndDefaultFilter returns the filter of a tenant's default resource with the given name.
func ndDefaultFilter(tenant, name string) string {
	return fmt.Sprintf("this.metadata.tenant == %q && this.metadata.name == %q && "+
		"this.metadata.labels[%q] == 'true'", tenant, name, ndDefaultLabel)
}
