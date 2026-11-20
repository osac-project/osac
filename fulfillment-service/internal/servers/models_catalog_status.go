package servers

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var applicationsResource = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// CatalogApps names the Argo CD ApplicationSets on the hub that deliver what
// git mode commits. Each generates one Application per cluster, named
// <ApplicationSet>-<cluster> and owned by the ApplicationSet. Empty fields
// take the defaults, the names of kahowell/ai-grid-tenant's
// apps/acm-models/hub/catalog-04-applicationsets.yaml.
type CatalogApps struct {
	// Namespace holds the ApplicationSets and their Applications
	// (AI_GRID_CATALOG_APP_NAMESPACE, default openshift-gitops).
	Namespace string
	// Models delivers each cluster's LLMInferenceServices
	// (AI_GRID_CATALOG_MODELS_APPSET).
	Models string
	// Ingress delivers every model's ExternalModel and MaaSModelRef
	// (AI_GRID_CATALOG_INGRESS_APPSET).
	Ingress string
	// Subscriptions delivers every MaaSSubscription and MaaSAuthPolicy
	// (AI_GRID_CATALOG_SUBSCRIPTIONS_APPSET).
	Subscriptions string
}

// The defaults for CatalogApps.
const (
	DefaultCatalogAppNamespace        = "openshift-gitops"
	DefaultCatalogModelsAppSet        = "acm-models-catalog-models"
	DefaultCatalogIngressAppSet       = "acm-models-catalog-ingress"
	DefaultCatalogSubscriptionsAppSet = "acm-models-catalog-subscriptions"
)

func (a CatalogApps) withDefaults() CatalogApps {
	for _, field := range []struct {
		value *string
		def   string
	}{
		{&a.Namespace, DefaultCatalogAppNamespace},
		{&a.Models, DefaultCatalogModelsAppSet},
		{&a.Ingress, DefaultCatalogIngressAppSet},
		{&a.Subscriptions, DefaultCatalogSubscriptionsAppSet},
	} {
		if *field.value == "" {
			*field.value = field.def
		}
	}
	return a
}

// CatalogSet picks one of the catalog's ApplicationSets.
type CatalogSet int

const (
	ModelsSet CatalogSet = iota
	IngressSet
	SubscriptionsSet
)

func (a CatalogApps) name(set CatalogSet) string {
	switch set {
	case ModelsSet:
		return a.Models
	case IngressSet:
		return a.Ingress
	default:
		return a.Subscriptions
	}
}

// The kinds git mode reads status for.
const (
	kindLLMInferenceService = "LLMInferenceService"
	kindExternalModel       = "ExternalModel"
	kindMaaSModelRef        = "MaaSModelRef"
	kindMaaSSubscription    = "MaaSSubscription"
	kindMaaSAuthPolicy      = "MaaSAuthPolicy"
	kindNetworkPolicy       = "NetworkPolicy"
)

// CatalogStatusReader reads what the spoke clusters report about the objects
// git mode delivers, from the health Argo CD records for each resource of the
// catalog's Applications on the hub (status.resources[]). The hub's Argo CD
// computes that health with its resource health checks, such as
// kahowell/ai-grid-tenant's for LLMInferenceService and the MaaS kinds.
type CatalogStatusReader struct {
	client dynamic.Interface
	apps   CatalogApps
	logger *slog.Logger
	// warned is set once a missing or forbidden list has been logged.
	warned atomic.Bool
}

// NewCatalogStatusReader returns a reader for the Applications apps names.
func NewCatalogStatusReader(client dynamic.Interface, apps CatalogApps) *CatalogStatusReader {
	return &CatalogStatusReader{client: client, apps: apps.withDefaults(), logger: slog.Default()}
}

// SetLogger sets where the reader warns that it can't list Applications.
func (r *CatalogStatusReader) SetLogger(logger *slog.Logger) *CatalogStatusReader {
	r.logger = logger
	return r
}

// ObjectState is what a cluster reports about one object.
type ObjectState int

const (
	// StatePending: not delivered yet, out of sync, or still progressing.
	StatePending ObjectState = iota
	// StateReady: in sync and healthy.
	StateReady
	// StateFailed: degraded, or its sync failed.
	StateFailed
)

// ObjectReport is one cluster's report about one object.
type ObjectReport struct {
	State ObjectState
	// Message is Argo CD's health or sync message, when it has one.
	Message string
	// Retrying: pending because its sync failed and Argo CD is retrying it,
	// such as an AppProject refusing the kind or namespace. Message then
	// says why.
	Retrying bool
	// NoSubscription: a MaaSModelRef whose backend is healthy but that no
	// active MaaSSubscription and MaaSAuthPolicy pair covers yet. Its state
	// is pending, because MaaS doesn't serve it to anyone yet.
	NoSubscription bool
	// Sync is whether Argo CD has applied the object as Git has it, apart
	// from its health; SyncMessage is the sync operation's message while
	// that sync failed or is being retried.
	Sync        SyncState
	SyncMessage string
	// Live is what the object on the cluster says, apart from its sync: an
	// object out of sync can still be the previous revision, running.
	// LiveMessage is its health message.
	Live        LiveState
	LiveMessage string
}

// SyncState is whether Argo CD has applied an object as Git has it.
type SyncState int

const (
	// SyncPending: not applied yet. The object is OutOfSync or Missing,
	// or Argo CD is still retrying its sync.
	SyncPending SyncState = iota
	// SyncSynced: the live object matches Git.
	SyncSynced
	// SyncFailed: Argo CD's sync to the revision it wants failed and it has
	// stopped retrying.
	SyncFailed
)

// LiveState is what an object on the cluster says, from its Argo CD health
// alone.
type LiveState int

const (
	// LiveAbsent: the object isn't on the cluster (health Missing, or no
	// health while it is out of sync).
	LiveAbsent LiveState = iota
	// LiveStarting: Progressing, Suspended, Unknown or no health, or
	// Degraded with a message that names a step still under way.
	LiveStarting
	// LiveReady: Healthy.
	LiveReady
	// LiveFailed: Degraded for any other reason.
	LiveFailed
	// LiveStopped: an LLMInferenceService KServe reports stopped, because
	// it carries the serving.kserve.io/stop annotation.
	LiveStopped
)

// ObjectReports maps each cluster that has an Application in the set to the
// objects of one kind it lists, by name. A cluster without an Application in
// the set isn't in the map. An object its Application doesn't list isn't on
// that cluster yet.
type ObjectReports map[string]map[string]ObjectReport

// Read returns, for each cluster with an Application in set, what it reports
// about the objects of kind. A missing or forbidden list (Argo CD isn't
// installed, or the ServiceAccount can't read Applications) reports nothing
// rather than failing, so git mode still answers from Git. A nil reader
// reports nothing. Read lists the namespace's Applications on every call: one
// request, for the handful of clusters a grid has.
func (r *CatalogStatusReader) Read(ctx context.Context, set CatalogSet, kind string) (ObjectReports, error) {
	if r == nil {
		return nil, nil
	}
	items, err := r.listApplications(ctx)
	if items == nil || err != nil {
		return nil, err
	}
	appSet := r.apps.name(set)
	out := ObjectReports{}
	for _, app := range items {
		cluster, ok := clusterOf(app, appSet)
		if !ok {
			continue
		}
		out[cluster] = applicationReports(app, kind)
	}
	return out, nil
}

// listApplications lists the Applications in the catalog's namespace. A
// missing or forbidden list (Argo CD isn't installed, or the ServiceAccount
// can't read Applications) returns none, warning once per process.
func (r *CatalogStatusReader) listApplications(ctx context.Context) ([]unstructured.Unstructured, error) {
	list, err := r.client.Resource(applicationsResource).Namespace(r.apps.Namespace).List(ctx, metav1.ListOptions{})
	if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
		// Once per process: every model then shows saved to Git, which is
		// easy to miss when the RBAC or Argo CD is what's missing.
		if r.warned.CompareAndSwap(false, true) {
			r.logger.WarnContext(ctx, "can't list the catalog's Argo CD Applications; models and subscriptions report no status",
				"namespace", r.apps.Namespace, "error", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list Applications in %s: %w", r.apps.Namespace, err)
	}
	if list.Items == nil {
		return []unstructured.Unstructured{}, nil
	}
	return list.Items, nil
}

// ObjectKey names one namespaced object by kind.
type ObjectKey struct {
	Kind      string
	Namespace string
	Name      string
}

// SharedResource is an object a model would write that an Application outside
// the catalog already delivers.
type SharedResource struct {
	Object      ObjectKey
	Application string
	// Pruning: the Application no longer wants the object, but Argo CD hasn't
	// pruned it, so it still carries that Application's tracking.
	Pruning bool
}

// SharedResources returns the objects that an Application in the catalog's
// namespace lists while none of the catalog's ApplicationSets own it, such
// as a sample model a GitOps repository deploys. The catalog's Applications
// set FailOnSharedResource, so Argo CD would refuse their copy. Only the
// catalog's namespace is read, and status.resources names no cluster, so an
// object delivered to another cluster still counts. A missing or forbidden
// list reports none, as Read does.
func (r *CatalogStatusReader) SharedResources(ctx context.Context, objects []ObjectKey) ([]SharedResource, error) {
	if r == nil || len(objects) == 0 {
		return nil, nil
	}
	items, err := r.listApplications(ctx)
	if err != nil {
		return nil, err
	}
	var out []SharedResource
	for _, app := range items {
		if r.isCatalogApp(app) {
			continue
		}
		resources, _, _ := unstructured.NestedSlice(app.Object, "status", "resources")
		for _, raw := range resources {
			res, _ := raw.(map[string]interface{})
			kind, _ := res["kind"].(string)
			namespace, _ := res["namespace"].(string)
			name, _ := res["name"].(string)
			key := ObjectKey{Kind: kind, Namespace: namespace, Name: name}
			if slices.Contains(objects, key) {
				pruning, _ := res["requiresPruning"].(bool)
				out = append(out, SharedResource{Object: key, Application: app.GetName(), Pruning: pruning})
			}
		}
	}
	slices.SortFunc(out, func(a, b SharedResource) int {
		return cmp.Or(
			strings.Compare(a.Application, b.Application),
			strings.Compare(a.Object.Kind, b.Object.Kind),
			strings.Compare(a.Object.Namespace, b.Object.Namespace),
			strings.Compare(a.Object.Name, b.Object.Name),
		)
	})
	return out, nil
}

// isCatalogApp reports whether one of the catalog's ApplicationSets
// generated app.
func (r *CatalogStatusReader) isCatalogApp(app unstructured.Unstructured) bool {
	for _, set := range []CatalogSet{ModelsSet, IngressSet, SubscriptionsSet} {
		if _, ok := clusterOf(app, r.apps.name(set)); ok {
			return true
		}
	}
	return false
}

// clusterOf returns the cluster an Application of ApplicationSet appSet is
// for: its name after "<appSet>-". An Application the ApplicationSet doesn't
// own isn't one of its, whatever its name, so a hand-made Application can't
// stand in for a cluster's catalog, and an ApplicationSet whose name extends
// another's doesn't count as that one.
func clusterOf(app unstructured.Unstructured, appSet string) (string, bool) {
	cluster, found := strings.CutPrefix(app.GetName(), appSet+"-")
	if !found || cluster == "" {
		return "", false
	}
	for _, owner := range app.GetOwnerReferences() {
		if owner.Kind == "ApplicationSet" && owner.Name == appSet {
			return cluster, true
		}
	}
	return "", false
}

// applicationReports reads the objects of kind in an Application's
// status.resources. A resource is ready when it is Synced and Healthy;
// failed when Degraded, or out of sync or missing after a failed sync; and
// pending otherwise (OutOfSync, Progressing, Missing, Suspended, Unknown, or
// no health). OutOfSync outranks Healthy: the live object is then the
// previous revision. A failed sync counts only when it was a sync to the
// revision the Application now wants, so an old failure doesn't fail an
// object edited since. While Argo CD retries a failed sync (phase Running,
// retryCount above zero), an object it hasn't synced is pending with the
// operation's message: an AppProject refusal shows only there, not in
// status.conditions, while the resource reads Missing and the Application
// Healthy. Once the retries run out the phase is Failed. Resources Argo CD is pruning (requiresPruning) are
// left out, and when a name repeats (the same object in two namespaces while
// the models namespace moves) the worse report wins.
func applicationReports(app unstructured.Unstructured, kind string) map[string]ObjectReport {
	syncFailed, retrying := false, false
	phase, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "phase")
	syncMessage, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "message")
	retries, _, _ := unstructured.NestedInt64(app.Object, "status", "operationState", "retryCount")
	tried, hasTried, _ := unstructured.NestedString(app.Object, "status", "operationState", "syncResult", "revision")
	wanted, _, _ := unstructured.NestedString(app.Object, "status", "sync", "revision")
	switch {
	case (phase == "Failed" || phase == "Error") && tried == wanted:
		syncFailed = true
	case phase == "Running" && retries > 0 && (!hasTried || tried == wanted):
		retrying = true
	}
	out := map[string]ObjectReport{}
	resources, _, _ := unstructured.NestedSlice(app.Object, "status", "resources")
	for _, raw := range resources {
		res, _ := raw.(map[string]interface{})
		if k, _ := res["kind"].(string); k != kind {
			continue
		}
		name, _ := res["name"].(string)
		if pruning, _ := res["requiresPruning"].(bool); name == "" || pruning {
			continue
		}
		synced, _ := res["status"].(string)
		health, _, _ := unstructured.NestedString(res, "health", "status")
		message, _, _ := unstructured.NestedString(res, "health", "message")
		report := ObjectReport{State: StatePending, Message: message}
		split := splitReport(kind, synced, health, message, syncFailed, retrying, syncMessage)
		switch {
		case health == "Degraded":
			report.State = StateFailed
			if synced == "Synced" || !syncFailed {
				report.State, report.NoSubscription = degradedUnderWay(kind, message)
				// Out of sync, the live object is the previous revision:
				// pending, whatever it says.
				report.NoSubscription = report.NoSubscription && synced == "Synced"
			}
		case synced != "Synced" && syncFailed:
			report = ObjectReport{State: StateFailed, Message: syncMessage}
		case synced != "Synced" && retrying:
			report = ObjectReport{State: StatePending, Message: syncMessage, Retrying: true}
		case synced == "Synced" && health == "Healthy":
			report.State = StateReady
		}
		report.Sync, report.SyncMessage, report.Live, report.LiveMessage = split.Sync, split.SyncMessage, split.Live, split.LiveMessage
		if previous, seen := out[name]; seen && worse(previous.State, report.State) {
			continue
		}
		out[name] = report
	}
	return out
}

// splitReport reads a resource's sync and its live state apart, for the
// fields ObjectReport keeps besides State. Sync: Synced is synced; failed
// when the sync to the wanted revision failed; pending otherwise, with the
// operation's message while Argo CD retries it. Live: Healthy is ready;
// Degraded is stopped, starting or failed by its message (degradedUnderWay);
// Missing, or no health while out of sync, is absent; anything else is
// starting.
func splitReport(kind, synced, health, message string, syncFailed, retrying bool, syncMessage string) ObjectReport {
	out := ObjectReport{Sync: SyncPending, Live: LiveStarting, LiveMessage: message}
	switch {
	case synced == "Synced":
		out.Sync = SyncSynced
	case syncFailed:
		out.Sync, out.SyncMessage = SyncFailed, syncMessage
	case retrying:
		out.SyncMessage = syncMessage
	}
	switch {
	case health == "Healthy":
		out.Live = LiveReady
	case health == "Degraded" && kind == kindLLMInferenceService && llmisvcStopped(message):
		out.Live = LiveStopped
	case health == "Degraded":
		if state, _ := degradedUnderWay(kind, message); state == StateFailed {
			out.Live = LiveFailed
		}
	case health == "Missing" || (health == "" && synced != "Synced"):
		out.Live, out.LiveMessage = LiveAbsent, ""
	}
	return out
}

// llmisvcStopped reports whether an LLMInferenceService's Ready message says
// KServe stopped it: with serving.kserve.io/stop "true", every workload and
// router condition is False with "Service is stopped" (opendatahub-io/kserve
// 5aabec9, pkg/controller/v1alpha2/llmisvc), and Ready carries it up.
func llmisvcStopped(message string) bool {
	return strings.Contains(strings.ToLower(message), "service is stopped")
}

// The Ready=False messages, by kind, that are steps still under way rather
// than failures. The catalog's Argo CD health checks (kahowell/ai-grid-tenant
// apps/gitops-config/manifests/argocd.yaml) map every Ready=False to
// Degraded and keep only the condition's message, so the message is all
// there is to tell them apart.
var (
	// KServe's LLMInferenceService copies the first unready step's message
	// into Ready (pkg/controller/v1alpha2/llmisvc, opendatahub-io/kserve
	// 5aabec9), lowercased here: a Deployment below its minimum availability
	// or rolling out, a ScaledObject, route, Gateway or inference pool not
	// accepted or ready yet. A message that dumps a route's or pool's
	// condition, "<type>=False (reason ...)", is a rejection and stays failed
	// (llmisvcRejected). A Deployment below its minimum availability has no
	// deadline: a pod that keeps crashing, can't pull its image or finds no
	// GPU leaves the same message, so it reads as starting too.
	llmisvcStartingMessages = []string{
		"does not have minimum availability",
		"rollout in progress",
		"is progressing",
		"has not been observed yet",
		"accepted them yet",
		"accepted it yet",
		"is not yet accepted by gateway",
		"scaledobject not yet visible",
		"scaledobject conditions not yet available",
		"scaledobject is not yet ready",
		"the following gateways are not ready",
		"is not ready", // "The inference pool <ns>/<name> is not ready"
		"waiting for",
	}
	// llmisvcRejected marks KServe's dump of a route's or pool's failed
	// condition, such as "ns/x: Accepted=False (reason ...)".
	llmisvcRejected = " (reason "
	// maas-controller's MaaSModelRef Ready messages for phase Pending and
	// Unhealthy (deriveModelPhase and its HTTPRoute and tenant steps,
	// opendatahub-io/models-as-a-service 05a5ff90). The first means the route
	// is healthy and only a subscription is missing. For an ExternalModel,
	// MaaS's "backend" is its HTTPRoute accepted by the gateway.
	maasAwaitingSubscription = "Awaiting governance pairing"
	maasPendingMessages      = []string{
		maasAwaitingSubscriptionAndBackend,
		"Governed but backend is not ready",
		"Waiting for HTTPRoute to be created",
	}
	// maas-controller's "Waiting for tenant resolution: <error>", also
	// pending: the route waits for the tenant.
	maasWaitingPrefix = "Waiting for tenant resolution:"
	// maas-controller's "Failed to reconcile HTTPRoute: ExternalModel <name>
	// not found in namespace <ns> ..." (providers_external.go): the
	// MaaSModelRef and its ExternalModel come in one Argo CD sync, and the
	// ref can be reconciled first. Pending, not failed: the next reconcile
	// finds it.
	maasExternalModelPrefix = "Failed to reconcile HTTPRoute: ExternalModel "
	maasNotFoundInNamespace = " not found in namespace "
	// The one pending message that also lacks a subscription.
	maasAwaitingSubscriptionAndBackend = "Awaiting governance pairing and backend readiness"
)

// degradedUnderWay reads the message of a Degraded resource of kind: pending
// when it names a step still under way (and, for a MaaSModelRef waiting only
// for a subscription, noSubscription), failed otherwise.
func degradedUnderWay(kind, message string) (state ObjectState, noSubscription bool) {
	switch kind {
	case kindLLMInferenceService:
		// Stopped on purpose: not serving, and not a failure.
		if llmisvcStopped(message) {
			return StatePending, false
		}
		if strings.Contains(message, llmisvcRejected) {
			return StateFailed, false
		}
		lower := strings.ToLower(message)
		for _, starting := range llmisvcStartingMessages {
			if strings.Contains(lower, starting) {
				return StatePending, false
			}
		}
	case kindMaaSModelRef:
		if message == maasAwaitingSubscription {
			return StatePending, true
		}
		if slices.Contains(maasPendingMessages, message) || strings.HasPrefix(message, maasWaitingPrefix) ||
			(strings.HasPrefix(message, maasExternalModelPrefix) && strings.Contains(message, maasNotFoundInNamespace)) {
			return StatePending, false
		}
	}
	return StateFailed, false
}

// worse reports whether state a is worse than b: failed, then pending, then
// ready.
func worse(a, b ObjectState) bool {
	rank := map[ObjectState]int{StateReady: 0, StatePending: 1, StateFailed: 2}
	return rank[a] > rank[b]
}
