package e2e

import (
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"kpt.dev/configsync/e2e/nomostest"
	"kpt.dev/configsync/e2e/nomostest/ntopts"
	nomostesting "kpt.dev/configsync/e2e/nomostest/testing"
	"kpt.dev/configsync/e2e/nomostest/testpredicates"
	"kpt.dev/configsync/e2e/nomostest/testwatcher"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/core/k8sobjects"
	"kpt.dev/configsync/pkg/kinds"
	"kpt.dev/configsync/pkg/metadata"
)

func TestAddIgnoreMutationObject(t *testing.T) {
	nt := nomostest.New(t, nomostesting.DriftControl, ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	nt.T.Log("Adding a new namespace")
	namespace := k8sobjects.NamespaceObject("bookstore", core.Annotation("season", "summer"))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a namespace"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Log("Add the ignore mutation to the namespace")
	updatedNamespace := k8sobjects.NamespaceObject("bookstore", core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", updatedNamespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("update namespace"))
	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), "bookstore", "",
		testwatcher.WatchPredicates(
			testpredicates.HasAnnotation("season", "summer"),
			testpredicates.HasAnnotationKey(metadata.LifecycleMutationAnnotation))))
}

func TestDeclareIgnoreMutationForUnmanagedObject(t *testing.T) {
	nt := nomostest.New(t, nomostesting.DriftControl, ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	nt.T.Log("Add an unmanaged namespace using kubectl")
	nsObj := k8sobjects.NamespaceObject("bookstore")
	nt.Must(rootSyncGitRepo.Add("ns.yaml", nsObj))
	nt.MustKubectl("apply", "-f", filepath.Join(rootSyncGitRepo.Root, "ns.yaml"))

	err := nt.Validate(nsObj.Name, "", &corev1.Namespace{})
	if err != nil {
		nt.T.Error(err)
	}

	nt.T.Log("Declare the unmanaged namespace with the ignore mutation annotation and other spec changes")
	namespace := k8sobjects.NamespaceObject(
		nsObj.Name,
		core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation),
		core.Annotation("season", "summer"))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a namespace"))
	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), nsObj.Name, "",
		testwatcher.WatchPredicates(testpredicates.HasAnnotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation), testpredicates.MissingAnnotation("season"))))
}

func TestDeclareExistingObjectWithAnnotation(t *testing.T) {
	nt := nomostest.New(t, nomostesting.DriftControl, ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	// Create nsObj with managed annotation using kubectl.
	nt.T.Log("Declare namespace with ignore annotation using kubectl ")
	nsObj := k8sobjects.NamespaceObject("bookstore",
		core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation),
	)
	nt.Must(rootSyncGitRepo.Add("ns.yaml", nsObj))
	nt.MustKubectl("apply", "-f", filepath.Join(rootSyncGitRepo.Root, "ns.yaml"))

	err := nt.Validate(nsObj.Name, "", &corev1.Namespace{})
	if err != nil {
		nt.T.Error(err)
	}

	nt.T.Log("Declare the namespace without the ignore mutation annotation")
	namespace := k8sobjects.NamespaceObject(
		nsObj.Name,
		core.Annotation("season", "summer"))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a namespace"))

	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), nsObj.Name, "",
		testwatcher.WatchPredicates(
			testpredicates.HasAnnotation("season", "summer"),
			testpredicates.MissingAnnotation(metadata.LifecycleMutationAnnotation),
		)))
}

// TODO: Is this test necessary?
func TestIgnoreObjectIsDeleted(t *testing.T) {
	nt := nomostest.New(t, nomostesting.DriftControl, ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	nt.T.Log("Adding namespace to Git")
	namespace := k8sobjects.NamespaceObject("bookstore",
		core.Annotation("season", "summer"), core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a namespace"))
	// The reason we need to stop the webhook here is that the webhook denies a request to modify Config Sync metadata
	// even if the resource has the `client.lifecycle.config.k8s.io/mutation` annotation.
	nomostest.StopWebhook(nt)

	// Test that the namespace exists with expected config management labels and annotations.
	nt.Must(nt.Validate(namespace.Name, "", &corev1.Namespace{},
		testpredicates.HasAllNomosMetadata()))

	nt.MustKubectl("delete", "ns", namespace.Name)

	// Remediator SHOULD recreate the configmap
	err := nt.Watcher.WatchForCurrentStatus(kinds.Namespace(), "bookstore", "")
	if err != nil {
		nt.T.Fatal(err)
	}

	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), namespace.Name, "",
		testwatcher.WatchPredicates(
			testpredicates.HasAnnotation("season", "summer"),
			testpredicates.HasAnnotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation),
		)))
}

func TestPruningIgnoredObject(t *testing.T) {
	nt := nomostest.New(t, nomostesting.DriftControl, ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	nt.T.Log("Adding namespace to Git")
	namespace := k8sobjects.NamespaceObject("bookstore",
		core.Annotation("season", "summer"), core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a namespace"))
	nt.Must(nt.WatchForAllSyncs())

	// prune the namespace
	nt.Must(rootSyncGitRepo.Remove("acme/ns.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("Prune namespace-bookstore"))
	// check for error
	nt.Must(nt.WatchForAllSyncs())

	// Should have been pruned
	nt.Must(nt.Watcher.WatchForNotFound(kinds.Namespace(), namespace.Name, ""))

	namespace = k8sobjects.NamespaceObject("bookstore",
		core.Annotation("season", "winter"), core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a namespace"))

	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), namespace.Name, "",
		testwatcher.WatchPredicates(
			testpredicates.HasAnnotation("season", "winter"),
			testpredicates.HasAnnotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation),
		)))
}

func TestDeclare(t *testing.T) {
	nt := nomostest.New(t, nomostesting.DriftControl, ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	nt.T.Log("Adding namespace to Git")
	namespace := k8sobjects.NamespaceObject("bookstore",
		core.Annotation("season", "summer"), core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a namespace"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Log("Remove label for declared namespace")
	updatedNamespace := k8sobjects.NamespaceObject("bookstore", core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", updatedNamespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("update namespace"))
	nt.Must(nt.WatchForAllSyncs())

	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), namespace.Name, "",
		testwatcher.WatchPredicates(
			testpredicates.HasAnnotation("season", "summer"),
			testpredicates.HasAnnotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation),
		)))

}

//TODO: Something that tests both applier and remediator?
// Can force resync on new commit

// Add, Update, Add (force resync), Delete, Prune

func TestAddUpdateAdd(t *testing.T) {
	nt := nomostest.New(t, nomostesting.DriftControl, ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	nt.T.Log("Adding namespace to Git")
	namespace := k8sobjects.NamespaceObject("bookstore",
		core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a namespace"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Log("Add annotation")
	updatedNamespace := k8sobjects.NamespaceObject("bookstore", core.Annotation(metadata.LifecycleMutationAnnotation, metadata.IgnoreMutation), core.Annotation("season", "summer"))
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", updatedNamespace))
	nt.Must(rootSyncGitRepo.CommitAndPush("update namespace"))
	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), "bookstore", "",
		testwatcher.WatchPredicates(
			testpredicates.MissingAnnotation("season"),
			testpredicates.HasAnnotationKey(metadata.LifecycleMutationAnnotation))))

	nt.T.Log("Modify using kubectl")
	nsObj := namespace.DeepCopy()
	nsObj.Annotations["season"] = "winter"
	nt.Must(rootSyncGitRepo.Add("unmanaged-ns.yaml", nsObj))
	nt.MustKubectl("apply", "-f", filepath.Join(rootSyncGitRepo.Root, "unmanaged-ns.yaml"))
	nt.Must(rootSyncGitRepo.Remove("unmanaged-ns.yaml"))

	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), nsObj.Name, "",
		testwatcher.WatchPredicates(
			testpredicates.HasAnnotation("season", "winter"),
		)))

	nsObj2 := k8sobjects.NamespaceObject("new-ns")
	nt.Must(rootSyncGitRepo.Add("acme/ns2.yaml", nsObj2))
	nt.Must(rootSyncGitRepo.CommitAndPush("add another namespace"))
	nt.Must(nt.WatchForAllSyncs())

	nt.Must(nt.Watcher.WatchObject(kinds.Namespace(), nsObj.Name, "",
		testwatcher.WatchPredicates(
			testpredicates.HasAnnotation("season", "winter"),
		)))

	// Manually modify
	//Force reapply
}
