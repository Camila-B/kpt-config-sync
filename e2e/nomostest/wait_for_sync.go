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

package nomostest

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"kpt.dev/configsync/e2e/nomostest/retry"
	"kpt.dev/configsync/e2e/nomostest/syncsource"
	"kpt.dev/configsync/e2e/nomostest/taskgroup"
	"kpt.dev/configsync/e2e/nomostest/testpredicates"
	"kpt.dev/configsync/e2e/nomostest/testwatcher"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/kinds"
	kstatus "sigs.k8s.io/cli-utils/pkg/kstatus/status"
)

type watchForAllSyncsOptions struct {
	readyCheck         bool
	syncRootRepos      bool
	syncNamespaceRepos bool
	skipRootRepos      map[string]bool
	skipNonRootRepos   map[types.NamespacedName]bool
	watchForSyncOpts   []WatchForSyncOption
}

// WatchForAllSyncsOptions is an optional parameter for WaitForRepoSyncs.
type WatchForAllSyncsOptions func(*watchForAllSyncsOptions)

// WithTimeout provides the timeout to WaitForRepoSyncs.
func WithTimeout(timeout time.Duration) WatchForAllSyncsOptions {
	return func(options *watchForAllSyncsOptions) {
		options.watchForSyncOpts = append(options.watchForSyncOpts,
			WithWatchOptions(testwatcher.WatchTimeout(timeout)))
	}
}

// Sha1Func is the function type that retrieves the commit sha1.
type Sha1Func func(nt *NT, nn types.NamespacedName) (string, error)

// RootSyncOnly specifies that only the RootRepos should be synced.
func RootSyncOnly() WatchForAllSyncsOptions {
	return func(options *watchForAllSyncsOptions) {
		options.syncNamespaceRepos = false
	}
}

// RepoSyncOnly specifies that only the NonRootRepos should be synced.
func RepoSyncOnly() WatchForAllSyncsOptions {
	return func(options *watchForAllSyncsOptions) {
		options.syncRootRepos = false
	}
}

// SkipRootRepos specifies RootSyncs which do not need to be synced.
func SkipRootRepos(skipRootRepos ...string) WatchForAllSyncsOptions {
	return func(options *watchForAllSyncsOptions) {
		options.skipRootRepos = make(map[string]bool)
		for _, skip := range skipRootRepos {
			options.skipRootRepos[skip] = true
		}
	}
}

// SkipNonRootRepos specifies RepoSyncs which do not need to be synced.
func SkipNonRootRepos(skipNonRootRepos ...types.NamespacedName) WatchForAllSyncsOptions {
	return func(options *watchForAllSyncsOptions) {
		options.skipNonRootRepos = make(map[types.NamespacedName]bool)
		for _, skip := range skipNonRootRepos {
			options.skipNonRootRepos[skip] = true
		}
	}
}

// SkipReadyCheck specifies not to wait for all Config Sync components to be ready.
func SkipReadyCheck() WatchForAllSyncsOptions {
	return func(options *watchForAllSyncsOptions) {
		options.readyCheck = false
	}
}

// SkipAllResourceGroupChecks is an optional parameter which specifies to skip
// ResourceGroup checks for all RSyncs after they finish syncing.
func SkipAllResourceGroupChecks() WatchForAllSyncsOptions {
	return func(options *watchForAllSyncsOptions) {
		options.watchForSyncOpts = append(options.watchForSyncOpts, SkipResourceGroupCheck())
	}
}

// WatchForAllSyncs calls WatchForSync on all Syncs in nt.SyncSources.
//
// If you want to validate specific fields of a Sync object, use
// nt.Watcher.WatchObject() instead.
func (nt *NT) WatchForAllSyncs(options ...WatchForAllSyncsOptions) error {
	nt.T.Helper()
	opts := watchForAllSyncsOptions{
		readyCheck:         true,
		syncRootRepos:      true,
		syncNamespaceRepos: true,
		skipRootRepos:      make(map[string]bool),
		skipNonRootRepos:   make(map[types.NamespacedName]bool),
		watchForSyncOpts:   []WatchForSyncOption{},
	}
	// Override defaults with specified options
	for _, option := range options {
		option(&opts)
	}

	if opts.readyCheck {
		if err := WaitForConfigSyncReady(nt); err != nil {
			return err
		}
	}

	tg := taskgroup.New()

	if opts.syncRootRepos {
		for id, source := range nt.SyncSources.RootSyncs() {
			if _, ok := opts.skipRootRepos[id.Name]; ok {
				continue
			}
			idPtr := id
			tg.Go(func() error {
				return nt.WatchForSync(
					kinds.RootSyncV1Beta1(), idPtr.Name, idPtr.Namespace, source,
					opts.watchForSyncOpts...)
			})
		}
	}

	if opts.syncNamespaceRepos {
		for id, source := range nt.SyncSources.RepoSyncs() {
			if _, ok := opts.skipNonRootRepos[id.ObjectKey]; ok {
				continue
			}
			idPtr := id
			tg.Go(func() error {
				return nt.WatchForSync(
					kinds.RepoSyncV1Beta1(), idPtr.Name, idPtr.Namespace, source,
					opts.watchForSyncOpts...)
			})
		}
	}

	return tg.Wait()
}

type watchForSyncOptions struct {
	watchOptions           []testwatcher.WatchOption
	skipResourceGroupCheck bool
}

// WatchForSyncOption is an optional parameter for WatchForSync.
type WatchForSyncOption func(*watchForSyncOptions)

// WithWatchOptions is an optional parameter to specify WatchOptions used for
// both the RSync watch and ResourceGroup watch.
func WithWatchOptions(watchOpts ...testwatcher.WatchOption) WatchForSyncOption {
	return func(options *watchForSyncOptions) {
		options.watchOptions = append(options.watchOptions, watchOpts...)
	}
}

// SkipResourceGroupCheck is an optional parameter to skip the ResourceGroup
// watch after the RSync finishes syncing.
func SkipResourceGroupCheck() WatchForSyncOption {
	return func(options *watchForSyncOptions) {
		options.skipResourceGroupCheck = true
	}
}

// WatchForSync watches the specified sync object until it's synced.
//
//   - gvk (required) is the sync object GroupVersionKind
//   - name (required) is the sync object name
//   - namespace (required) is the sync object namespace
//   - source (required) is the expectations for this RSync.
//   - opts (optional) allows configuring the watcher (e.g. timeout)
func (nt *NT) WatchForSync(
	gvk schema.GroupVersionKind,
	name, namespace string,
	source syncsource.SyncSource,
	options ...WatchForSyncOption,
) error {
	nt.T.Helper()
	opts := watchForSyncOptions{
		skipResourceGroupCheck: false,
		watchOptions:           []testwatcher.WatchOption{},
	}
	// Override defaults with specified options
	for _, option := range options {
		option(&opts)
	}
	if namespace == "" {
		// If namespace is empty, use the default namespace
		namespace = configsync.ControllerNamespace
	}

	predicates := []testpredicates.Predicate{
		// Wait until status.observedGeneration matches metadata.generation
		testpredicates.HasObservedLatestGeneration(nt.Scheme),
		// Wait until metadata.deletionTimestamp is missing, and conditions do not iniclude Reconciling=True or Stalled=True
		testpredicates.StatusEquals(nt.Scheme, kstatus.CurrentStatus),
	}

	expectedSyncPath := source.Path()
	expectedCommit, err := source.Commit()
	if err != nil {
		return err
	}

	switch gvk.Kind {
	case configsync.RootSyncKind:
		// Wait until expected commit/version is parsed, rendered, and synced
		predicates = append(predicates, RootSyncHasStatusSyncCommit(expectedCommit))
		// Wait until expected directory/chart-name is parsed, rendered, and synced
		predicates = append(predicates, RootSyncHasStatusSyncPath(expectedSyncPath))
	case configsync.RepoSyncKind:
		// Wait until expected commit/version is parsed, rendered, and synced
		predicates = append(predicates, RepoSyncHasStatusSyncCommit(expectedCommit))
		// Wait until expected directory/chart-name is parsed, rendered, and synced
		predicates = append(predicates, RepoSyncHasStatusSyncPath(expectedSyncPath))
	default:
		return retry.NewTerminalError(
			fmt.Errorf("%w: got %s, want RootSync or RepoSync",
				testpredicates.ErrWrongType, gvk.Kind))
	}

	rsyncWatchOptions := []testwatcher.WatchOption{
		testwatcher.WatchPredicates(predicates...),
	}
	rsyncWatchOptions = append(rsyncWatchOptions, opts.watchOptions...)

	err = nt.Watcher.WatchObject(gvk, name, namespace, rsyncWatchOptions...)
	if err != nil {
		return fmt.Errorf("waiting for sync: %w", err)
	}
	nt.T.Logf("%s %s/%s is synced", gvk.Kind, namespace, name)
	if opts.skipResourceGroupCheck {
		return nil
	}
	rgWatchOptions := []testwatcher.WatchOption{
		testwatcher.WatchPredicates(resourceGroupHasReconciled(expectedCommit, nt.Scheme)),
	}
	rgWatchOptions = append(rgWatchOptions, opts.watchOptions...)
	if err := nt.Watcher.WatchObject(kinds.ResourceGroup(), name, namespace, rgWatchOptions...); err != nil {
		return fmt.Errorf("waiting for ResourceGroup: %w", err)
	}
	return nil
}
