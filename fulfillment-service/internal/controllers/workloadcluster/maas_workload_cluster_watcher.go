package workloadcluster

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type managedClusterSignal struct {
	hubID string
	name  string
}

var errMaaSWatchExpired = errors.New("hub ManagedCluster watch expired")
var errMaaSSignalRejected = errors.New("workload cluster signal rejected")

type watchFailureObserver struct {
	watch.Interface
	onError    func()
	onExpire   func()
	once       sync.Once
	resultChan chan watch.Event
	stop       chan struct{}
	stopOnce   sync.Once
}

func newWatchFailureObserver(delegate watch.Interface, onError, onExpire func()) *watchFailureObserver {
	w := &watchFailureObserver{
		Interface:  delegate,
		onError:    onError,
		onExpire:   onExpire,
		resultChan: make(chan watch.Event),
		stop:       make(chan struct{}),
	}
	go func() {
		defer close(w.resultChan)
		results := w.Interface.ResultChan()
		for {
			select {
			case <-w.stop:
				return
			case event, ok := <-results:
				if !ok {
					return
				}
				if event.Type == watch.Error {
					w.once.Do(func() {
						if isWatchExpiration(apierrors.FromObject(event.Object)) {
							w.onExpire()
						} else {
							w.onError()
						}
					})
				}
				select {
				case <-w.stop:
					return
				case w.resultChan <- event:
				}
			}
		}
	}()
	return w
}

func isWatchExpiration(err error) bool {
	return apierrors.IsGone(err) || apierrors.IsResourceExpired(err)
}

func (w *watchFailureObserver) ResultChan() <-chan watch.Event {
	return w.resultChan
}

func (w *watchFailureObserver) Stop() {
	w.stopOnce.Do(func() {
		close(w.stop)
		w.Interface.Stop()
	})
}

type unavailableSignalKey struct {
	hubID          string
	registrationID string
}

type unavailableSignalState struct {
	generation uint64
	scanID     uint64
	cancel     context.CancelFunc
}

// ensureMaaSWatcher starts one informer per referenced Hub. The informer is shared
// by all WorkloadClusters registered against that Hub and only emits opaque
// resource IDs through the private Signal RPC.
func (r *function) ensureMaaSWatcher(parent context.Context, hubID, credentialFingerprint string) (string, error) {
	if hubID == "" {
		return "", nil
	}
	for {
		r.watchersLock.Lock()
		if _, exists := r.watchers[hubID]; exists {
			if credentialFingerprint != "" && r.watcherCredentials[hubID] == credentialFingerprint {
				resolvedFingerprint := r.watcherCredentials[hubID]
				r.watcherUseRevisions[hubID]++
				r.watchersLock.Unlock()
				return resolvedFingerprint, nil
			}
		}
		observedRevision := r.watcherUseRevisions[hubID]
		r.watchersLock.Unlock()

		// Resolve once before starting the watcher. The same snapshot is used to
		// construct the client and to identify the credentials for this watcher.
		startupCtx, cancelStartup := context.WithTimeout(parent, 10*time.Second)
		kubeconfig, err := r.resolveMaaSKubeconfig(startupCtx, hubID)
		cancelStartup()
		if err != nil {
			r.logger.Warn("workload cluster Hub watcher could not resolve credentials", slog.String("component", "workload-cluster-controller"), slog.String("operation", "watch"), slog.String("reason", "HubCredentialResolutionFailed"))
			return "", errors.New("hub credential resolution failed")
		}
		resolvedFingerprint := fmt.Sprintf("%x", sha256.Sum256(kubeconfig))

		r.watchersLock.Lock()
		if observedRevision != r.watcherUseRevisions[hubID] {
			r.watchersLock.Unlock()
			if parent.Err() != nil {
				return "", parent.Err()
			}
			continue
		}
		if current, exists := r.watchers[hubID]; exists {
			if r.watcherCredentials[hubID] == resolvedFingerprint {
				r.watcherUseRevisions[hubID]++
				r.watchersLock.Unlock()
				return resolvedFingerprint, nil
			}
			// The Hub credential changed. Stop the old informer before installing
			// the watcher built from the resolved snapshot.
			current()
			delete(r.watchers, hubID)
			delete(r.watcherCredentials, hubID)
		}
		if current, exists := r.unavailableSignals[hubID]; exists {
			current.cancel()
			delete(r.unavailableSignals, hubID)
		}
		ctx, cancel := context.WithCancel(parent)
		r.watchers[hubID] = cancel
		r.watcherCredentials[hubID] = resolvedFingerprint
		r.watcherGenerations[hubID]++
		r.watcherUseRevisions[hubID]++
		generation := r.watcherGenerations[hubID]
		r.setWatcherReadyLocked(hubID, false)
		r.watchFailures[hubID] = 0
		r.watchFailureObserved[hubID] = false
		r.watchHealthy[hubID] = false
		r.watchEstablished[hubID] = false
		r.watchersLock.Unlock()

		go func() {
			reconnecting := false
			expirationBackoff := 250 * time.Millisecond
			defer func() {
				r.watchersLock.Lock()
				if r.watcherGenerations[hubID] == generation {
					r.setWatcherReadyLocked(hubID, false)
					cancel()
					delete(r.watchers, hubID)
					delete(r.watcherCredentials, hubID)
					delete(r.watchFailures, hubID)
					delete(r.watchFailureObserved, hubID)
					delete(r.watchFailureAttempts, hubID)
					delete(r.watchHealthy, hubID)
					delete(r.watchEstablished, hubID)
				}
				r.watchersLock.Unlock()
				r.cancelUnavailableSignalsForGeneration(hubID, generation)
			}()
			for ctx.Err() == nil {
				err := r.startMaaSWatcher(ctx, hubID, generation, reconnecting, kubeconfig, &expirationBackoff)
				if err == nil || ctx.Err() != nil {
					return
				}
				if errors.Is(err, errMaaSWatchExpired) {
					timer := time.NewTimer(expirationBackoff)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
					if expirationBackoff < 5*time.Second {
						expirationBackoff *= 2
						if expirationBackoff > 5*time.Second {
							expirationBackoff = 5 * time.Second
						}
					}
					reconnecting = true
					continue
				}
				r.watchersLock.Lock()
				if r.watcherGenerations[hubID] == generation {
					r.setWatcherReadyLocked(hubID, false)
					r.watchHealthy[hubID] = false
					r.watchFailures[hubID] = generation
					r.watchFailureObserved[hubID] = true
				}
				r.watchersLock.Unlock()
				refreshCtx, cancelRefresh := context.WithTimeout(ctx, 10*time.Second)
				refreshedKubeconfig, refreshErr := r.resolveMaaSKubeconfig(refreshCtx, hubID)
				cancelRefresh()
				r.watchersLock.Lock()
				if r.watcherGenerations[hubID] != generation {
					r.watchersLock.Unlock()
					return
				}
				if refreshErr == nil {
					kubeconfig = refreshedKubeconfig
					fingerprint := fmt.Sprintf("%x", sha256.Sum256(kubeconfig))
					if r.watcherCredentials[hubID] != fingerprint {
						r.watcherCredentials[hubID] = fingerprint
						r.watcherUseRevisions[hubID]++
					}
				}
				reconnecting = r.watchEstablished[hubID]
				r.setWatcherReadyLocked(hubID, false)
				r.watchFailures[hubID] = 0
				r.watchersLock.Unlock()
				if ctx.Err() == nil {
					go r.signalHubUnavailable(ctx, hubID, generation)
				}
				r.logger.Warn("workload cluster Hub watcher stopped", slog.String("component", "workload-cluster-controller"), slog.String("operation", "watch"), slog.String("reason", "HubUnreachable"))
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
		return resolvedFingerprint, nil
	}
}

func (r *function) resolveMaaSKubeconfig(ctx context.Context, hubID string) ([]byte, error) {
	hubResponse, err := privatev1.NewHubsClient(r.connection()).Get(ctx, privatev1.HubsGetRequest_builder{Id: hubID}.Build())
	if err != nil {
		return nil, err
	}
	secrets := privatev1.NewSecretsClient(r.connection())
	return controllers.ResolveHubKubeconfig(ctx, hubResponse.GetObject().GetSpec(), func(ctx context.Context, id string) (*privatev1.Secret, error) {
		response, err := secrets.Get(ctx, privatev1.SecretsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return nil, err
		}
		return response.GetObject(), nil
	})
}

func (r *function) startMaaSWatcher(ctx context.Context, hubID string, generation uint64, reconnecting bool, kubeconfig []byte, expirationBackoff *time.Duration) error {
	r.watchersLock.Lock()
	if r.watcherGenerations[hubID] != generation || ctx.Err() != nil {
		r.watchersLock.Unlock()
		return nil
	}
	if r.watchAttemptIDs == nil {
		r.watchAttemptIDs = make(map[string]uint64)
	}
	r.watchAttemptIDs[hubID]++
	attemptID := r.watchAttemptIDs[hubID]
	r.watchFailures[hubID] = 0
	r.watchersLock.Unlock()
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resource := client.Resource(managedClusterResourceGVR())
	watchEstablished := make(chan struct{})
	var watchEstablishedOnce sync.Once
	watchExpired := make(chan struct{})
	var watchExpiredOnce sync.Once
	watchHasEstablished := func() bool {
		select {
		case <-watchEstablished:
			return true
		default:
			return false
		}
	}
	markWatchExpired := func() {
		if !watchHasEstablished() {
			r.reportMaaSWatchFailure(ctx, hubID, generation, attemptID, cancel)
			return
		}
		r.watchersLock.Lock()
		if r.watcherGenerations[hubID] == generation && r.watchAttemptIDs[hubID] == attemptID {
			r.setWatcherReadyLocked(hubID, false)
			r.watchHealthy[hubID] = false
		}
		r.watchersLock.Unlock()
		watchExpiredOnce.Do(func() {
			close(watchExpired)
			cancel()
		})
	}
	informer := cache.NewSharedIndexInformer(&cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			return resource.List(ctx, options)
		},
		WatchFuncWithContext: func(requestCtx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			result, err := resource.Watch(requestCtx, options)
			if err != nil {
				streamingList := options.SendInitialEvents != nil && *options.SendInitialEvents
				if isWatchExpiration(err) {
					markWatchExpired()
				} else if !streamingList {
					r.reportMaaSWatchFailure(ctx, hubID, generation, attemptID, cancel)
				}
				return nil, err
			}
			watchEstablishedOnce.Do(func() { close(watchEstablished) })
			return newWatchFailureObserver(result, func() {
				r.reportMaaSWatchFailure(ctx, hubID, generation, attemptID, cancel)
			}, markWatchExpired), nil
		},
	}, &unstructured.Unstructured{}, 0, cache.Indexers{})
	signalQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[managedClusterSignal]())
	defer signalQueue.ShutDown()
	for i := 0; i < 4; i++ {
		go r.runManagedClusterSignalWorker(watchCtx, signalQueue)
	}
	if err := informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		if isWatchExpiration(err) {
			markWatchExpired()
		} else {
			r.reportMaaSWatchFailure(ctx, hubID, generation, attemptID, cancel)
		}
	}); err != nil {
		return err
	}
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(object interface{}) { enqueueManagedClusterSignal(signalQueue, hubID, object) },
		UpdateFunc: func(_, object interface{}) { enqueueManagedClusterSignal(signalQueue, hubID, object) },
		DeleteFunc: func(object interface{}) { enqueueManagedClusterSignal(signalQueue, hubID, object) },
	}); err != nil {
		return err
	}
	go informer.Run(watchCtx.Done())
	initialSyncCtx, cancelInitialSync := context.WithTimeout(watchCtx, 30*time.Second)
	syncResult := make(chan bool, 1)
	go func() { syncResult <- cache.WaitForCacheSync(initialSyncCtx.Done(), informer.HasSynced) }()
	synced, established := false, false
	watchEstablishedCh := watchEstablished
	for !synced || !established {
		select {
		case synced = <-syncResult:
		case <-watchEstablishedCh:
			established = true
			watchEstablishedCh = nil
		case <-watchCtx.Done():
			cancelInitialSync()
			select {
			case <-watchExpired:
				return errMaaSWatchExpired
			default:
			}
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("hub ManagedCluster informer stopped during initial sync")
		case <-initialSyncCtx.Done():
			synced = false
			established = false
		}
		if !synced && initialSyncCtx.Err() != nil {
			break
		}
	}
	cancelInitialSync()
	if !synced || !established {
		select {
		case <-watchExpired:
			return errMaaSWatchExpired
		default:
		}
		if ctx.Err() != nil {
			return nil
		}
		r.watchersLock.Lock()
		if r.watcherGenerations[hubID] == generation && r.watchAttemptIDs[hubID] == attemptID {
			r.watchFailures[hubID] = generation
			r.watchHealthy[hubID] = false
			r.watchFailureObserved[hubID] = true
		}
		r.watchersLock.Unlock()
		if watchCtx.Err() == nil {
			go r.signalHubUnavailable(ctx, hubID, generation)
		}
		cancel()
		return errors.New("hub ManagedCluster informer did not establish a watch")
	}
	r.watchersLock.Lock()
	if r.watcherGenerations[hubID] != generation || r.watchAttemptIDs[hubID] != attemptID || r.watchFailures[hubID] == generation || watchCtx.Err() != nil {
		r.watchersLock.Unlock()
		select {
		case <-watchExpired:
			return errMaaSWatchExpired
		default:
		}
		return errors.New("hub ManagedCluster informer became unavailable during sync")
	}
	r.setWatcherReadyLocked(hubID, true)
	*expirationBackoff = 250 * time.Millisecond
	r.watchHealthy[hubID] = true
	r.managedCaches[hubID] = informer
	r.watchEstablished[hubID] = true
	if current, exists := r.unavailableSignals[hubID]; exists && current.generation == generation {
		current.cancel()
		delete(r.unavailableSignals, hubID)
	}
	r.watchersLock.Unlock()
	go r.monitorMaaSHealth(watchCtx, ctx, client, hubID, generation, attemptID, cancel)
	defer func() {
		r.watchersLock.Lock()
		if r.watcherGenerations[hubID] == generation && r.watchAttemptIDs[hubID] == attemptID {
			r.watchHealthy[hubID] = false
		}
		if current := r.managedCaches[hubID]; current == informer {
			delete(r.managedCaches, hubID)
		}
		r.watchersLock.Unlock()
	}()
	if reconnecting && r.ownsWatcher(hubID, generation) {
		go r.signalForHub(watchCtx, hubID)
	} else if r.ownsWatcher(hubID, generation) {
		// Initial cache sync must trigger reconciliation without being reported as
		// a reconnect event. A signal without metadata is an internal wake-up.
		go r.signalForHub(watchCtx, hubID)
	}
	<-watchCtx.Done()
	select {
	case <-watchExpired:
		return errMaaSWatchExpired
	default:
	}
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("hub ManagedCluster watch stopped")
}

func (r *function) ownsWatcher(hubID string, generation uint64) bool {
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	return r.watcherGenerations[hubID] == generation
}

func (r *function) monitorMaaSHealth(watchCtx, signalParent context.Context, client dynamic.Interface, hubID string, generation, attemptID uint64, cancel context.CancelFunc) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-watchCtx.Done():
			return
		case <-ticker.C:
			probeCtx, cancelProbe := context.WithTimeout(watchCtx, 5*time.Second)
			_, err := client.Resource(managedClusterResourceGVR()).List(probeCtx, metav1.ListOptions{Limit: 1})
			cancelProbe()
			if err == nil || watchCtx.Err() != nil {
				continue
			}

			r.watchersLock.Lock()
			ownsWatcher := r.watcherGenerations[hubID] == generation && r.watchAttemptIDs[hubID] == attemptID
			if ownsWatcher {
				r.setWatcherReadyLocked(hubID, false)
				r.watchHealthy[hubID] = false
				r.watchFailures[hubID] = generation
				r.watchFailureObserved[hubID] = true
			}
			r.watchersLock.Unlock()
			if ownsWatcher {
				r.logger.Warn("workload cluster Hub health check failed", slog.String("component", "workload-cluster-controller"), slog.String("operation", "health-check"), slog.String("reason", "HubUnreachable"))
				go r.signalHubUnavailable(signalParent, hubID, generation)
				cancel()
			}
			return
		}
	}
}

func (r *function) reportMaaSWatchFailure(ctx context.Context, hubID string, generation, attemptID uint64, cancel context.CancelFunc) {
	r.watchersLock.Lock()
	ownsWatcher := r.watcherGenerations[hubID] == generation &&
		r.watchAttemptIDs[hubID] == attemptID &&
		r.watchFailureAttempts[hubID] != attemptID
	if ownsWatcher {
		r.setWatcherReadyLocked(hubID, false)
		r.watchHealthy[hubID] = false
		r.watchFailures[hubID] = generation
		r.watchFailureObserved[hubID] = true
		r.watchFailureAttempts[hubID] = attemptID
	}
	r.watchersLock.Unlock()
	if ownsWatcher {
		r.logger.Warn("workload cluster Hub watch failed", slog.String("component", "workload-cluster-controller"), slog.String("operation", "watch"), slog.String("reason", "HubUnreachable"))
		go r.signalHubUnavailable(ctx, hubID, generation)
		cancel()
	}
}

func (r *function) signalHubUnavailable(parent context.Context, hubID string, generation uint64) {
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	if r.watcherGenerations[hubID] != generation || r.readyHubs[hubID] {
		return
	}
	if r.unavailableSignals == nil {
		r.unavailableSignals = make(map[string]unavailableSignalState)
	}
	if current, exists := r.unavailableSignals[hubID]; exists && current.generation == generation {
		return
	}
	if current, exists := r.unavailableSignals[hubID]; exists {
		current.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	r.unavailableSignalID++
	scanID := r.unavailableSignalID
	r.unavailableSignals[hubID] = unavailableSignalState{generation: generation, scanID: scanID, cancel: cancel}

	go func() {
		defer func() {
			r.watchersLock.Lock()
			if current, exists := r.unavailableSignals[hubID]; exists && current.generation == generation && current.scanID == scanID {
				delete(r.unavailableSignals, hubID)
			}
			r.watchersLock.Unlock()
		}()
		r.runUnavailableSignalQueue(ctx, hubID)
	}()
}

func (r *function) cancelUnavailableSignalsForGeneration(hubID string, generation uint64) {
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	if current, exists := r.unavailableSignals[hubID]; exists && current.generation == generation {
		current.cancel()
		delete(r.unavailableSignals, hubID)
	}
}

func (r *function) runUnavailableSignalQueue(ctx context.Context, hubID string) {
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[unavailableSignalKey]())
	defer queue.ShutDown()
	var pendingLock sync.Mutex
	pending := make(map[unavailableSignalKey]struct{})
	delivered := make(map[unavailableSignalKey]struct{})
	sweepSeen := make(map[unavailableSignalKey]struct{})
	enqueue := func(registrationID string) {
		item := unavailableSignalKey{hubID: hubID, registrationID: registrationID}
		pendingLock.Lock()
		if _, exists := pending[item]; exists {
			pendingLock.Unlock()
			return
		}
		if _, exists := delivered[item]; exists {
			pendingLock.Unlock()
			return
		}
		pending[item] = struct{}{}
		pendingLock.Unlock()
		queue.Add(item)
	}
	forget := func(item unavailableSignalKey, success bool) {
		queue.Forget(item)
		pendingLock.Lock()
		delete(pending, item)
		if success {
			delivered[item] = struct{}{}
		}
		pendingLock.Unlock()
	}
	const workerCount = 8
	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				item, shutdown := queue.Get()
				if shutdown {
					return
				}
				if ctx.Err() != nil {
					queue.Done(item)
					return
				}
				err := r.signalWorkloadCluster(ctx, item.registrationID)
				if err != nil && ctx.Err() == nil {
					queue.AddRateLimited(item)
				} else {
					forget(item, err == nil)
				}
				queue.Done(item)
			}
		}()
	}

	lastID := ""
	for ctx.Err() == nil {
		ids, nextID, err := r.listWorkloadClusterPage(ctx, hubID, lastID)
		for _, id := range ids {
			sweepSeen[unavailableSignalKey{hubID: hubID, registrationID: id}] = struct{}{}
			enqueue(id)
		}
		if err != nil {
			r.logger.Warn("Failed to find workload clusters for Hub", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "ListFailed"))
			lastID = ""
			sweepSeen = make(map[unavailableSignalKey]struct{})
		} else if nextID != "" {
			lastID = nextID
			continue
		} else {
			pendingLock.Lock()
			for item := range delivered {
				if _, present := sweepSeen[item]; !present {
					delete(delivered, item)
				}
			}
			pendingLock.Unlock()
			lastID = ""
			sweepSeen = make(map[unavailableSignalKey]struct{})
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			queue.ShutDown()
			workers.Wait()
			return
		case <-timer.C:
		}
	}
}

func (r *function) readyMaaSInformer(hubID string) (cache.SharedIndexInformer, uint64, uint64, bool, bool) {
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	informer := r.managedCaches[hubID]
	generation := r.watcherGenerations[hubID]
	readinessRevision := r.watcherReadyRevision[hubID]
	ready := r.readyHubs[hubID] && r.watchHealthy[hubID] && informer != nil
	pending := r.watchers[hubID] != nil && !r.watchEstablished[hubID] && !r.watchFailureObserved[hubID]
	return informer, generation, readinessRevision, ready, pending
}

func (r *function) isReadyMaaSInformer(hubID string, generation, readinessRevision uint64, informer cache.SharedIndexInformer, credentialFingerprint string) bool {
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	return r.watcherGenerations[hubID] == generation && r.watcherReadyRevision[hubID] == readinessRevision && r.readyHubs[hubID] && r.watchHealthy[hubID] && r.managedCaches[hubID] == informer && r.watcherCredentials[hubID] == credentialFingerprint
}

func (r *function) setWatcherReadyLocked(hubID string, ready bool) {
	if r.readyHubs[hubID] == ready {
		return
	}
	r.readyHubs[hubID] = ready
	r.watcherReadyRevision[hubID]++
}

func (r *function) isMaaSWatcherCredential(hubID, credentialFingerprint string) bool {
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	return r.watcherCredentials[hubID] == credentialFingerprint
}

func (r *function) runManagedClusterSignalWorker(ctx context.Context, queue workqueue.TypedRateLimitingInterface[managedClusterSignal]) {
	for {
		item, shutdown := queue.Get()
		if shutdown {
			return
		}
		err := r.signalForEvent(ctx, item.hubID, item.name)
		if err != nil && ctx.Err() == nil {
			queue.AddRateLimited(item)
		} else {
			queue.Forget(item)
		}
		queue.Done(item)
	}
}

func enqueueManagedClusterSignal(queue workqueue.TypedRateLimitingInterface[managedClusterSignal], hubID string, object interface{}) {
	meta, ok := object.(metav1.Object)
	if !ok || meta.GetName() == "" {
		if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
			meta, _ = tombstone.Obj.(metav1.Object)
		}
	}
	if meta == nil || meta.GetName() == "" {
		return
	}
	queue.Add(managedClusterSignal{hubID: hubID, name: meta.GetName()})
}

func (r *function) signalForEvent(parent context.Context, hubID, managedClusterName string) error {
	filter := "this.spec.maas.source_hub_id == " + strconv.Quote(hubID) + " && this.spec.maas.managed_cluster_name == " + strconv.Quote(managedClusterName)
	for attempt := 0; attempt < 5; attempt++ {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		response, err := r.workloadClusters.List(ctx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter, Limit: proto.Int32(100)}.Build())
		cancel()
		if err != nil {
			if parent.Err() != nil {
				return parent.Err()
			}
			timer := time.NewTimer(time.Duration(1<<attempt) * 250 * time.Millisecond)
			select {
			case <-parent.Done():
				timer.Stop()
				return parent.Err()
			case <-timer.C:
			}
			continue
		}
		for _, item := range response.GetItems() {
			if err := r.signalWorkloadCluster(parent, item.GetId()); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("failed to list workload clusters for Hub event")
}

func (r *function) signalForHub(ctx context.Context, hubID string) {
	const workerCount = 8
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[unavailableSignalKey]())
	defer queue.ShutDown()
	var pendingLock sync.Mutex
	pending := make(map[unavailableSignalKey]struct{})
	scanDone := false
	scanFinished := make(chan struct{})
	var scanFinishedOnce sync.Once
	finishScan := func() {
		pendingLock.Lock()
		scanDone = true
		if len(pending) == 0 {
			scanFinishedOnce.Do(func() { close(scanFinished) })
		}
		pendingLock.Unlock()
	}
	forget := func(item unavailableSignalKey) {
		queue.Forget(item)
		pendingLock.Lock()
		delete(pending, item)
		if scanDone && len(pending) == 0 {
			scanFinishedOnce.Do(func() { close(scanFinished) })
		}
		pendingLock.Unlock()
	}
	enqueue := func(id string) {
		item := unavailableSignalKey{hubID: hubID, registrationID: id}
		pendingLock.Lock()
		if _, exists := pending[item]; exists {
			pendingLock.Unlock()
			return
		}
		pending[item] = struct{}{}
		pendingLock.Unlock()
		queue.Add(item)
	}

	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				item, shutdown := queue.Get()
				if shutdown {
					return
				}
				if ctx.Err() != nil {
					queue.Done(item)
					return
				}
				err := r.signalWorkloadCluster(ctx, item.registrationID)
				if err != nil && ctx.Err() == nil {
					queue.AddRateLimited(item)
				} else {
					forget(item)
				}
				queue.Done(item)
			}
		}()
	}
	defer func() {
		queue.ShutDown()
		workers.Wait()
	}()

	lastID := ""
	for attempt := 0; ; attempt++ {
		ids, nextID, err := r.listWorkloadClusterPage(ctx, hubID, lastID)
		for _, id := range ids {
			enqueue(id)
		}
		if err == nil && nextID == "" {
			finishScan()
			select {
			case <-ctx.Done():
			case <-scanFinished:
			}
			return
		}
		if err == nil {
			lastID = nextID
			attempt = -1
			continue
		}
		if ctx.Err() != nil {
			return
		}
		r.logger.Warn("Failed to find workload clusters for Hub", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "ListFailed"))
		backoff := time.Duration(1<<min(attempt, 5)) * 250 * time.Millisecond
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *function) listWorkloadClusterPage(ctx context.Context, hubID, lastID string) ([]string, string, error) {
	filter := "this.spec.maas.source_hub_id == " + strconv.Quote(hubID)
	if lastID != "" {
		filter += " && this.id > " + strconv.Quote(lastID)
	}
	var response *privatev1.WorkloadClustersListResponse
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		response, err = r.workloadClusters.List(listCtx, privatev1.WorkloadClustersListRequest_builder{Filter: &filter, Limit: proto.Int32(100)}.Build())
		cancel()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, lastID, ctx.Err()
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, lastID, ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		return nil, lastID, err
	}
	ids := make([]string, 0, len(response.GetItems()))
	for _, item := range response.GetItems() {
		if item.GetId() == "" {
			return nil, lastID, errors.New("workload cluster list returned an item without an ID")
		}
		ids = append(ids, item.GetId())
	}
	if len(ids) == 0 {
		return ids, "", nil
	}
	return ids, ids[len(ids)-1], nil
}

func (r *function) signalWorkloadClusterUntilDone(ctx context.Context, id string) {
	backoff := time.Second
	for {
		if err := r.signalWorkloadCluster(ctx, id); err == nil || ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < time.Minute {
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
		}
	}
}

func (r *function) signalWorkloadCluster(ctx context.Context, id string) error {
	backoff := 250 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, signalErr := r.workloadClusters.Signal(attemptCtx, privatev1.WorkloadClustersSignalRequest_builder{Id: id}.Build())
		cancel()
		if status.Code(signalErr) == codes.PermissionDenied {
			return errMaaSSignalRejected
		}
		if signalErr == nil || ctx.Err() != nil || isPermanentSignalError(signalErr) {
			if signalErr != nil && ctx.Err() == nil {
				r.logger.Warn("Failed to signal workload cluster", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "SignalFailed"))
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < time.Minute {
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
		}
	}
	r.logger.Warn("Failed to signal workload cluster", slog.String("component", "workload-cluster-controller"), slog.String("operation", "signal"), slog.String("reason", "SignalFailed"))
	return errors.New("failed to signal workload cluster")
}

func isPermanentSignalError(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument, codes.PermissionDenied:
		return true
	default:
		return false
	}
}

func managedClusterResourceGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: managedClusterGroup, Version: managedClusterVersion, Resource: managedClusterResource}
}

// connection is kept as a narrow accessor so the watcher cannot accidentally
// expose the gRPC connection in logs or condition messages.
func (r *function) connection() *grpc.ClientConn { return r.grpcClient }
