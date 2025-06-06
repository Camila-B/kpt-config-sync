// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package watch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/declared"
	"kpt.dev/configsync/pkg/diff"
	"kpt.dev/configsync/pkg/metrics"
	"kpt.dev/configsync/pkg/remediator/conflict"
	"kpt.dev/configsync/pkg/remediator/queue"
	"kpt.dev/configsync/pkg/status"
	syncerclient "kpt.dev/configsync/pkg/syncer/client"
	"kpt.dev/configsync/pkg/util/log"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Copying strategy from k8s.io/client-go/tools/cache/reflector.go
	// We try to spread the load on apiserver by setting timeouts for
	// watch requests - it is random in [minWatchTimeout, 2*minWatchTimeout].
	minWatchTimeout = 5 * time.Minute

	// RESTConfigTimeout sets the REST config timeout for the remediator to 1 hour.
	//
	// RESTConfigTimeout should be longer than 2*minWatchTimeout to respect
	// the watch timeout set by `ListOptions.TimeoutSeconds` in the watch
	// create requests.
	RESTConfigTimeout = time.Hour

	// ClientWatchDecodingCause is the status cause returned for client errors during response decoding.
	// https://github.com/kubernetes/client-go/blob/v0.26.7/rest/request.go#L785
	ClientWatchDecodingCause metav1.CauseType = "ClientWatchDecoding"

	// ContextCancelledCauseMessage is the error message from the DynamicClient
	// when the Watch method errors due to context cancellation.
	// https://github.com/kubernetes/apimachinery/blob/v0.26.7/pkg/watch/streamwatcher.go#L120
	ContextCancelledCauseMessage = "unable to decode an event from the watch stream: context canceled"
)

// maxWatchRetryFactor is used to determine when the next retry should happen.
// 2^^18 * time.Millisecond = 262,144 ms, which is about 4.36 minutes.
const maxWatchRetryFactor = 18

// Runnable defines the custom watch interface.
type Runnable interface {
	Stop()
	Run(ctx context.Context) status.Error
	SetLatestCommit(string)
}

const (
	watchEventBookmarkType    = "Bookmark"
	watchEventErrorType       = "Error"
	watchEventUnsupportedType = "Unsupported"
)

// errorLoggingInterval specifies the minimal time interval two errors related to the same object
// and having the same errorType should be logged.
const errorLoggingInterval = time.Second

// filteredWatcher is wrapper around a watch interface.
// It only keeps the events for objects that are
// - either present in the declared resources,
// - or managed by the same reconciler.
type filteredWatcher struct {
	gvk        schema.GroupVersionKind
	startWatch WatchFunc
	resources  *declared.Resources
	queue      *queue.ObjectQueue
	scope      declared.Scope
	syncName   string
	// errorTracker maps an error to the time when the same error happened last time.
	errorTracker    map[string]time.Time
	conflictHandler conflict.Handler

	// The following fields are guarded by the mutex.
	mux     sync.Mutex
	base    watch.Interface
	stopped bool
	// commitMux prevents simultaneous reads/writes on latestCommit
	commitMux    sync.RWMutex
	latestCommit string
}

// filteredWatcher implements the Runnable interface.
var _ Runnable = &filteredWatcher{}

// NewFiltered returns a new filtered watcher initialized with the given options.
func NewFiltered(cfg watcherConfig) Runnable {
	return &filteredWatcher{
		gvk:             cfg.gvk,
		startWatch:      cfg.startWatch,
		resources:       cfg.resources,
		queue:           cfg.queue,
		scope:           cfg.scope,
		syncName:        cfg.syncName,
		base:            watch.NewEmptyWatch(),
		errorTracker:    make(map[string]time.Time),
		conflictHandler: cfg.conflictHandler,
		latestCommit:    cfg.commit,
	}
}

// pruneErrors removes the errors happened before errorLoggingInterval from w.errorTracker.
// This is to save the memory usage for tracking errors.
func (w *filteredWatcher) pruneErrors() {
	for errName, lastErrorTime := range w.errorTracker {
		if time.Since(lastErrorTime) >= errorLoggingInterval {
			delete(w.errorTracker, errName)
		}
	}
}

// addError checks whether an error identified by the errorID has been tracked,
// and handles it in one of the following ways:
//   - tracks it if it has not yet been tracked;
//   - updates the time for this error to time.Now() if `errorLoggingInterval` has passed
//     since the same error happened last time;
//   - ignore the error if `errorLoggingInterval` has NOT passed since it happened last time.
//
// addError returns false if the error is ignored, and true if it is not ignored.
func (w *filteredWatcher) addError(errorID string) bool {
	lastErrorTime, ok := w.errorTracker[errorID]
	if !ok || time.Since(lastErrorTime) >= errorLoggingInterval {
		w.errorTracker[errorID] = time.Now()
		return true
	}
	return false
}

// SetLatestCommit sets the latest observed commit which references the GVK
// watched by this filteredWatcher.
func (w *filteredWatcher) SetLatestCommit(commit string) {
	defer w.commitMux.Unlock()
	w.commitMux.Lock()
	w.latestCommit = commit
}

func (w *filteredWatcher) getLatestCommit() string {
	defer w.commitMux.RUnlock()
	w.commitMux.RLock()
	return w.latestCommit
}

// Stop fully stops the filteredWatcher in a threadsafe manner. This means that
// it stops the underlying base watch and prevents the filteredWatcher from
// restarting it (like it does if the API server disconnects the base watch).
func (w *filteredWatcher) Stop() {
	w.mux.Lock()
	defer w.mux.Unlock()

	w.base.Stop()
	w.stopped = true
}

// This function is borrowed from https://github.com/kubernetes/client-go/blob/master/tools/cache/reflector.go.
func isExpiredError(err error) bool {
	// In Kubernetes 1.17 and earlier, the api server returns both apierrors.StatusReasonExpired and
	// apierrors.StatusReasonGone for HTTP 410 (Gone) status code responses. In 1.18 the kube server is more consistent
	// and always returns apierrors.StatusReasonExpired. For backward compatibility we can only remove the apierrors.IsGone
	// check when we fully drop support for Kubernetes 1.17 servers from reflectors.
	return apierrors.IsResourceExpired(err) || apierrors.IsGone(err)
}

// TODO: Use wait.ExponentialBackoff in the watch retry logic
func waitUntilNextRetry(retries int) {
	if retries > maxWatchRetryFactor {
		retries = maxWatchRetryFactor
	}
	milliseconds := int64(math.Pow(2, float64(retries)))
	duration := time.Duration(milliseconds) * time.Millisecond
	time.Sleep(duration)
}

// isContextCancelledStatusError returns true if the error is a *StatusError and
// one of the detail cause reasons is `ClientWatchDecoding` with the message
// `unable to decode an event from the watch stream: context canceled`.
// StatusError doesn't implement Is or Unwrap methods, and all http client
// errors are returned as decoding errors, so we have to test the cause message
// to detect context cancellation.
// This explicitly does not test for context timeout, because that's used for
// http client timeout, which we want to retry, and we don't currently have any
// timeout on the parent context.
func isContextCancelledStatusError(err error) bool {
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		if message, found := findStatusErrorCauseMessage(statusErr, ClientWatchDecodingCause); found {
			if message == ContextCancelledCauseMessage {
				return true
			}
		}
	}
	return false
}

// findStatusErrorCauseMessage returns the message and true, if the StatusError
// has a .detail.cause[] entry that matches the specified type, otherwise
// returns false.
func findStatusErrorCauseMessage(statusErr *apierrors.StatusError, causeType metav1.CauseType) (string, bool) {
	if statusErr == nil || statusErr.ErrStatus.Details == nil {
		return "", false
	}
	for _, cause := range statusErr.ErrStatus.Details.Causes {
		if cause.Type == causeType {
			return cause.Message, true
		}
	}
	return "", false
}

// Run reads the event from the base watch interface,
// filters the event and pushes the object contained
// in the event to the controller work queue.
func (w *filteredWatcher) Run(ctx context.Context) status.Error {
	klog.Infof("Remediator watch started for %s", w.gvk)
	var resourceVersion string
	var retriesForWatchError int
	var runErr status.Error

Watcher:
	for {
		// There are three ways start can return:
		// 1. false, error -> We were unable to start the watch, so exit Run().
		// 2. false, nil   -> We have been stopped via Stop(), so exit Run().
		// 3. true,  nil   -> We have not been stopped and we started a new watch.
		started, err := w.start(ctx, resourceVersion)
		if err != nil {
			return err
		}
		if !started {
			break
		}

		eventCount := 0
		ignoredEventCount := 0
		klog.V(2).Infof("Remediator (re)starting watch for %s at resource version %q", w.gvk, resourceVersion)
		eventCh := w.base.ResultChan()
	EventHandler:
		for {
			select {
			case <-ctx.Done():
				runErr = status.InternalWrapf(ctx.Err(), "remediator watch stopped for %s", w.gvk)
				// Stop the watcher & return the status error
				break Watcher
			case event, ok := <-eventCh:
				if !ok { // channel closed
					// Restart the watcher
					break EventHandler
				}
				w.pruneErrors()
				newVersion, ignoreEvent, err := w.handle(ctx, event)
				eventCount++
				if ignoreEvent {
					ignoredEventCount++
				}
				if err != nil {
					if status.IsContextCanceledError(err) || isContextCancelledStatusError(err) {
						// The error wrappers are especially confusing for
						// users, so just return context.Canceled.
						runErr = status.InternalWrapf(context.Canceled, "remediator watch stopped for %s", w.gvk)
						// Stop the watcher & return the status error
						break Watcher
					}
					if isExpiredError(err) {
						klog.Infof("Remediator watch for %s at resource version %q closed with: %v", w.gvk, resourceVersion, err)
						// `w.handle` may fail because we try to watch an old resource version, setting
						// a watch on an old resource version will always fail.
						// Reset `resourceVersion` to an empty string here so that we can start a new
						// watch at the most recent resource version.
						resourceVersion = ""
					} else if w.addError(watchEventErrorType + errorID(err)) {
						klog.Errorf("Remediator watch for %s at resource version %q ended with: %v", w.gvk, resourceVersion, err)
					}
					retriesForWatchError++
					waitUntilNextRetry(retriesForWatchError)
					// Restart the watcher
					break EventHandler
				}
				retriesForWatchError = 0
				if newVersion != "" {
					resourceVersion = newVersion
				}
			}
		}
		klog.V(2).Infof("Remediator watch ending for %s at resource version %q (total events: %d, ignored events: %d)",
			w.gvk, resourceVersion, eventCount, ignoredEventCount)
	}
	klog.Infof("Remediator watch stopped for %s", w.gvk)
	return runErr
}

// start initiates a new base watch at the given resource version in a
// threadsafe manner and returns true if the new base watch was created. Returns
// false if the filteredWatcher is already stopped and returns error if the base
// watch could not be started.
func (w *filteredWatcher) start(ctx context.Context, resourceVersion string) (bool, status.Error) {
	w.mux.Lock()
	defer w.mux.Unlock()

	if w.stopped {
		return false, nil
	}
	w.base.Stop()

	// We want to avoid situations of hanging watchers. Stop any watchers that
	// do not receive any events within the timeout window.
	timeoutSeconds := int64(minWatchTimeout.Seconds() * (rand.Float64() + 1.0))
	options := metav1.ListOptions{
		AllowWatchBookmarks: true,
		ResourceVersion:     resourceVersion,
		TimeoutSeconds:      &timeoutSeconds,
		Watch:               true,
	}

	base, err := w.startWatch(ctx, options)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, status.InternalWrapf(err, "failed to start remediator watch for %s", w.gvk)
		} else if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			statusErr := syncerclient.ConflictWatchResourceDoesNotExist(err, w.gvk)
			klog.Warningf("Remediator encountered a resource conflict: "+
				"%v. To resolve the conflict, the remediator will enqueue a resync "+
				"and restart the resource watch after the sync has succeeded.", statusErr)
			metrics.RecordResourceConflict(ctx, w.getLatestCommit())
			return false, statusErr
		}
		return false, status.APIServerErrorf(err, "failed to start remediator watch for %s", w.gvk)
	}
	w.base = base
	return true, nil
}

func errorID(err error) string {
	errTypeName := reflect.TypeOf(err).String()

	var s string
	switch t := err.(type) {
	case *apierrors.StatusError:
		if t == nil {
			break
		}
		if t.ErrStatus.Details != nil {
			s = t.ErrStatus.Details.Name
		}
		if s == "" {
			s = fmt.Sprintf("%s-%s-%d", t.ErrStatus.Status, t.ErrStatus.Reason, t.ErrStatus.Code)
		}
	}
	return errTypeName + s
}

// handle reads the event from the base watch interface,
// filters the event and pushes the object contained
// in the event to the controller work queue.
//
// handle returns the new resource version, whether the event should be ignored,
// and an error indicating that a watch.Error event type is encountered and the
// watch should be restarted.
func (w *filteredWatcher) handle(ctx context.Context, event watch.Event) (string, bool, error) {
	klog.Infof("Remediator handling watch event %v %v", event.Type, w.gvk)
	var deleted bool
	switch event.Type {
	case watch.Added, watch.Modified:
		deleted = false
	case watch.Deleted:
		deleted = true
	case watch.Bookmark:
		m, err := meta.Accessor(event.Object)
		if err != nil {
			// For watch.Bookmark, only the ResourceVersion field of event.Object is set.
			// Therefore, set the second argument of w.addError to watchEventBookmarkType.
			if w.addError(watchEventBookmarkType) {
				klog.Errorf("Remediator unable to access metadata of Bookmark event: %v", event)
			}
			return "", false, nil
		}
		return m.GetResourceVersion(), false, nil
	case watch.Error:
		return "", false, apierrors.FromObject(event.Object)
	// Keep the default case to catch any new watch event types added in the future.
	default:
		if w.addError(watchEventUnsupportedType) {
			klog.Errorf("Remediator encountered unsupported watch event: %#v", event)
		}
		return "", false, nil
	}

	// get client.Object from the runtime object.
	object, ok := event.Object.(client.Object)
	if !ok {
		klog.Warningf("Remediator received non client.Object in watch event: %T", object)
		metrics.RecordInternalError(ctx, "remediator")
		return "", false, nil
	}

	if klog.V(5).Enabled() {
		klog.V(5).Infof("Remediator received watch event for object: %q (generation: %d): %s",
			core.IDOf(object), object.GetGeneration(), log.AsJSON(object))
	} else {
		klog.V(3).Infof("Remediator received watch event for object: %q (generation: %d)",
			core.IDOf(object), object.GetGeneration())
	}

	// filter objects.
	if !w.shouldProcess(object) {
		klog.V(4).Infof("Remediator ignoring event for object: %q (generation: %d)",
			core.IDOf(object), object.GetGeneration())
		return object.GetResourceVersion(), true, nil
	}

	if deleted {
		klog.V(2).Infof("Remediator received watch event for deleted object %q (generation: %d)",
			core.IDOf(object), object.GetGeneration())
		object = queue.MarkDeleted(ctx, object)
	} else {
		klog.V(2).Infof("Remediator received watch event for created/updated object %q (generation: %d)",
			core.IDOf(object), object.GetGeneration())
	}

	// Update drifted objects in the Resources ignored cache
	if _, found := w.resources.GetIgnored(core.IDOf(object)); found {
		w.resources.UpdateIgnored(object)
		klog.V(3).Infof("Updating object '%v' in the ignore mutation cache", core.GKNN(object))
	}
	w.queue.Add(object)
	return object.GetResourceVersion(), false, nil
}

// shouldProcess returns true if the given object should be enqueued by the
// watcher for processing.
func (w *filteredWatcher) shouldProcess(object client.Object) bool {
	id := core.IDOf(object)

	// Process the resource if we are the manager regardless if it is declared or not.
	if diff.IsManager(w.scope, w.syncName, object) {
		return true
	}

	decl, commit, found := w.resources.GetDeclared(id)
	if !found {
		// The resource is neither declared nor managed by the same reconciler, so don't manage it.
		return false
	}

	// If the object is declared, we only process it if it has the same GVK as
	// its declaration. Otherwise we expect to get another event for the same
	// object but with a matching GVK so we can actually compare it to its
	// declaration.
	currentGVK := object.GetObjectKind().GroupVersionKind()
	declaredGVK := decl.GroupVersionKind()
	if currentGVK != declaredGVK {
		klog.V(5).Infof("Remediator received a watch event for object %q with kind %s, which does not match the declared kind %s. ",
			id, currentGVK, declaredGVK)
		return false
	}

	if diff.CanManage(w.scope, w.syncName, object, diff.OperationManage) {
		return true
	}

	desiredManager := declared.ResourceManager(w.scope, w.syncName)
	conflictErr := status.ManagementConflictErrorWrap(object, desiredManager)
	conflict.Record(context.Background(), w.conflictHandler, conflictErr, commit)
	return false
}
