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

package e2e

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"kpt.dev/configsync/e2e/nomostest"
	"kpt.dev/configsync/e2e/nomostest/ntopts"
	"kpt.dev/configsync/e2e/nomostest/policy"
	"kpt.dev/configsync/e2e/nomostest/taskgroup"
	nomostesting "kpt.dev/configsync/e2e/nomostest/testing"
	"kpt.dev/configsync/e2e/nomostest/testpredicates"
	"kpt.dev/configsync/e2e/nomostest/testwatcher"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/api/configsync/v1beta1"
	"kpt.dev/configsync/pkg/applier"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/core/k8sobjects"
	"kpt.dev/configsync/pkg/declared"
	"kpt.dev/configsync/pkg/kinds"
	"kpt.dev/configsync/pkg/metadata"
	kstatus "sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/cli-utils/pkg/object/dependson"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This file includes e2e tests for sync ordering.
// The sync ordering feature is only supported in the multi-repo mode.

func TestMultiDependencies(t *testing.T) {
	rootSyncID := nomostest.DefaultRootSyncID
	nt := nomostest.New(t, nomostesting.Lifecycle,
		ntopts.SyncWithGitSource(rootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(rootSyncID)

	namespaceName := "bookstore"
	nt.T.Logf("Remove the namespace %q if it already exists", namespaceName)
	nt.MustKubectl("delete", "ns", namespaceName, "--ignore-not-found")

	nt.T.Log("A new test: verify that an object is created after its dependency (cm1 and cm2 both are in the Git repo, but don't exist on the cluster. cm2 depends on cm1.)")
	nt.T.Logf("Add the namespace, cm1, and cm2 (cm2 depends on cm1)")
	namespace := k8sobjects.NamespaceObject(namespaceName)
	cm1Name := "cm1"
	cm2Name := "cm2"
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", namespace))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName))))
	// cm2 depends on cm1
	nt.Must(rootSyncGitRepo.Add("acme/cm2.yaml", k8sobjects.ConfigMapObject(core.Name(cm2Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm1"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Add the namespace, cm1, and cm2 (cm2 depends on cm1)"))
	nt.Must(nt.WatchForAllSyncs())

	ns := &corev1.Namespace{}
	nt.Must(nt.KubeClient.Get(namespaceName, "", ns))

	cm1 := &corev1.ConfigMap{}
	nt.Must(nt.KubeClient.Get(cm1Name, namespaceName, cm1))

	cm2 := &corev1.ConfigMap{}
	nt.Must(nt.KubeClient.Get(cm2Name, namespaceName, cm2))

	nt.T.Logf("Verify that the namespace is created before the configmaps in it")
	if cm1.CreationTimestamp.Before(&ns.CreationTimestamp) {
		nt.T.Fatalf("a namespace (%s) should be created before a ConfigMap (%s) in it", core.GKNN(ns), core.GKNN(cm1))
	}

	if cm2.CreationTimestamp.Before(&ns.CreationTimestamp) {
		nt.T.Fatalf("a namespace (%s) should be created before a ConfigMap (%s) in it", core.GKNN(ns), core.GKNN(cm2))
	}

	nt.T.Logf("Verify that cm1 is created before cm2")
	if cm2.CreationTimestamp.Before(&cm1.CreationTimestamp) {
		nt.T.Fatalf("an object (%s) should be created after its dependency (%s)", core.GKNN(cm2), core.GKNN(cm1))
	}

	nt.T.Logf("Verify that cm2 has the dependsOn annotation")
	nt.Must(nt.Validate(cm2Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.HasAnnotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm1")))

	// There are 2 configmaps in the namespace at this point: cm1, cm2.
	// The dependency graph is:
	//   * cm2 depends on cm1
	nt.T.Log("A new test: verify that an object can declare dependency on an existing object (cm1 and cm3 both are in the Git repo, cm3 depends on cm1. cm1 already exists on the cluster, cm3 does not.)")
	nt.T.Log("Add cm3, which depends on an existing object, cm1")
	// cm3 depends on cm1
	cm3Name := "cm3"
	nt.Must(rootSyncGitRepo.Add("acme/cm3.yaml", k8sobjects.ConfigMapObject(core.Name(cm3Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm1"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("add cm3, which depends on an existing object, cm1"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Logf("Verify that cm1 is created before cm3")
	cm1 = &corev1.ConfigMap{}
	nt.Must(nt.KubeClient.Get(cm1Name, namespaceName, cm1))

	cm3 := &corev1.ConfigMap{}
	nt.Must(nt.KubeClient.Get(cm3Name, namespaceName, cm3))
	if cm3.CreationTimestamp.Before(&cm1.CreationTimestamp) {
		nt.T.Fatalf("an object (%s) should be created after its dependency (%s)", core.GKNN(cm3), core.GKNN(cm1))
	}

	// There are 3 configmaps in the namespace at this point: cm1, cm2, cm3
	// The dependency graph is:
	//   * cm2 depends on cm1
	//   * cm3 depends on cm1

	nt.T.Log("A new test: verify that an existing object can declare dependency on a non-existing object (cm1 and cm0 both are in the Git repo, cm1 depends on cm0. cm1 already exists on the cluster, cm0 does not.)")
	nt.T.Log("add a new configmap, cm0; and add the dependsOn annotation to cm1")
	// cm1 depends on cm0
	cm0Name := "cm0"
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("add a new configmap, cm0; and add the dependsOn annotation to cm1"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Log("Verify that cm1 is created before cm0")
	cm1 = &corev1.ConfigMap{}
	nt.Must(nt.KubeClient.Get(cm1Name, namespaceName, cm1))

	cm0 := &corev1.ConfigMap{}
	nt.Must(nt.KubeClient.Get(cm3Name, namespaceName, cm0))
	if cm0.CreationTimestamp.Before(&cm1.CreationTimestamp) {
		nt.T.Fatalf("Declaring the dependency of an existing object (%s) on a non-existing object (%s) should not cause the existing object to be recreated", core.GKNN(cm1), core.GKNN(cm0))
	}

	// There are 4 configmaps in the namespace at this point: cm0, cm1, cm2, cm3
	// The dependency graph is:
	//   * cm1 depends on cm0
	//   * cm2 depends on cm1
	//   * cm3 depends on cm1

	nt.T.Log("A new test: verify that Config Sync reports an error when a cyclic dependency is encountered (a cyclic dependency between cm0, cm1, and cm2. cm1 depends on cm0; cm2 depends on cm1; cm0 depends on cm2)")
	nt.T.Log("Create a cyclic dependency between cm0, cm1, and cm2")
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm2"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Create a cyclic dependency between cm0, cm1, and cm2"))
	nt.Must(nt.Watcher.WatchForRootSyncSyncError(configsync.RootSyncName, applier.ApplierErrorCode, "cyclic dependency", nil))

	nt.T.Log("Verify that cm0 does not have the dependsOn annotation")
	nt.Must(nt.Validate(cm0Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.MissingAnnotation(dependson.Annotation)))

	nt.T.Log("Remove the cyclic dependency from the Git repo")
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Remove the cyclic dependency from the Git repo"))
	nt.Must(nt.WatchForAllSyncs())

	// There are 4 configmaps in the namespace at this point: cm0, cm1, cm2, cm3.
	// The dependency graph is:
	//   * cm1 depends on cm0
	//   * cm2 depends on cm1
	//   * cm3 depends on cm1

	nt.T.Log("A new test: verify that an object can be removed without affecting its dependency (cm3 depends on cm1, and both cm3 and cm1 exist in the Git repo and on the cluster.)")
	nt.T.Log("Remove cm3")
	nt.Must(rootSyncGitRepo.Remove("acme/cm3.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("Remove cm3"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Log("Verify that cm3 is removed")
	nt.Must(nt.Watcher.WatchForNotFound(kinds.ConfigMap(), cm3Name, namespaceName))

	nt.T.Log("Verify that cm1 is still on the cluster")
	nt.Must(nt.Validate(cm1Name, namespaceName, &corev1.ConfigMap{}))

	// There are 3 configmaps in the namespace at this point: cm0, cm1, cm2.
	// The dependency graph is:
	//   * cm1 depends on cm0
	//   * cm2 depends on cm1

	nt.T.Log("A new test: verify that an object and its dependency can be removed together (cm1 and cm2 both exist in the Git repo and on the cluster. cm2 depends on cm1.)")
	nt.T.Log("Remove cm1 and cm2")
	nt.Must(rootSyncGitRepo.Remove("acme/cm1.yaml"))
	nt.Must(rootSyncGitRepo.Remove("acme/cm2.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("Remove cm1 and cm2"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Log("Verify that cm1 is removed")
	nt.Must(nt.Watcher.WatchForNotFound(kinds.ConfigMap(), cm1Name, namespaceName))

	nt.T.Log("Verify that cm2 is removed")
	nt.Must(nt.Watcher.WatchForNotFound(kinds.ConfigMap(), cm2Name, namespaceName))

	// There are 1 configmap in the namespace at this point: cm0.

	nt.T.Log("Add cm1, cm2, and cm3")
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"))))
	nt.Must(rootSyncGitRepo.Add("acme/cm2.yaml", k8sobjects.ConfigMapObject(core.Name(cm2Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"))))
	nt.Must(rootSyncGitRepo.Add("acme/cm3.yaml", k8sobjects.ConfigMapObject(core.Name(cm3Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Add cm1, cm2, and cm3"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Logf("Verify that cm1 has the dependsOn annotation, and depends on cm0")
	nt.Must(nt.Validate(cm1Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.HasAnnotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0")))

	nt.T.Logf("Verify that cm2 has the dependsOn annotation, and depends on cm0")
	nt.Must(nt.Validate(cm2Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.HasAnnotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0")))

	nt.T.Logf("Verify that cm3 has the dependsOn annotation, and depends on cm0")
	nt.Must(nt.Validate(cm3Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.HasAnnotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0")))

	// There are 4 configmaps in the namespace at this point: cm0, cm1, cm2 and cm3.
	// The dependency graph is:
	//   * cm1 depends on cm0
	//   * cm2 depends on cm0
	//   * cm3 depends on cm0

	nt.T.Log("A new test: verify that an object can be disabled without affecting its dependency")
	nt.T.Log("Disable cm3 by adding the `configmanagement.gke.io/managed: disabled` annotation")
	nt.Must(rootSyncGitRepo.Add("acme/cm3.yaml", k8sobjects.ConfigMapObject(core.Name(cm3Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"),
		metadata.WithManagementMode(metadata.ManagementDisabled))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Disable cm3 by adding the `configmanagement.gke.io/managed: disabled` annotation"))
	nt.Must(nt.WatchForAllSyncs())

	rsObj := &v1beta1.RootSync{}
	nt.Must(nt.KubeClient.Get(rootSyncID.Name, rootSyncID.Namespace, rsObj))
	applySetID := core.GetLabel(rsObj, metadata.ApplySetParentIDLabel)

	nt.T.Log("Verify that cm3 no longer has the CS metadata")
	nt.Must(nt.Validate(cm3Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.NoConfigSyncMetadata(),
		testpredicates.MissingLabel(metadata.ApplySetPartOfLabel)))

	nt.T.Log("Verify that cm0 still has the CS metadata")
	nt.Must(nt.Validate(cm0Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.HasAllNomosMetadata(),
		testpredicates.HasLabel(metadata.ApplySetPartOfLabel, applySetID)))

	// There are 4 configmaps in the namespace at this point: cm0, cm1, cm2 and cm3.
	// The inventory tracks 3 configmaps: cm0, cm1, cm2. The dependency graph is:
	//   * cm1 depends on cm0
	//   * cm2 depends on cm0

	nt.T.Log("A new test: verify that the dependsOn annotation can be removed from an object without affecting its dependency")
	nt.T.Log("Remove the dependsOn annotation from cm2")
	nt.Must(rootSyncGitRepo.Add("acme/cm2.yaml", k8sobjects.ConfigMapObject(core.Name(cm2Name), core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Remove the dependsOn annotation from cm2"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Log("Verify that cm2 no longer has the dependsOn annotation")
	nt.Must(nt.Validate(cm2Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.MissingAnnotation(dependson.Annotation)))

	// There are 4 configmaps in the namespace at this point: cm0, cm1, cm2 and cm3.
	// The inventory tracks 3 configmaps: cm0, cm1, cm2. The dependency graph is:
	//   * cm1 depends on cm0

	nt.T.Log("A new test: verify that an object and its dependency can be disabled together")
	nt.T.Log("Disable both cm1 and cm0 by adding the `configmanagement.gke.io/managed: disabled` annotation")
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName),
		metadata.WithManagementMode(metadata.ManagementDisabled))))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"),
		metadata.WithManagementMode(metadata.ManagementDisabled))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Disable both cm1 and cm0 by adding the `configmanagement.gke.io/managed: disabled` annotation"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Log("Verify that cm1 no longer has the CS metadata")
	nt.Must(nt.Validate(cm1Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.NoConfigSyncMetadata(),
		testpredicates.MissingLabel(metadata.ApplySetPartOfLabel)))

	nt.T.Log("Verify that cm0 no longer has the CS metadata")
	nt.Must(nt.Validate(cm0Name, namespaceName, &corev1.ConfigMap{},
		testpredicates.NoConfigSyncMetadata(),
		testpredicates.MissingLabel(metadata.ApplySetPartOfLabel)))

}

func TestExternalDependencyError(t *testing.T) {
	nt := nomostest.New(t, nomostesting.Lifecycle,
		ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	namespaceName := "bookstore"
	nt.T.Logf("Remove the namespace %q if it already exists", namespaceName)
	nt.MustKubectl("delete", "ns", namespaceName, "--ignore-not-found")
	cm0Name := "cm0"
	cm1Name := "cm1"

	// TestCase: cm1 depends on cm0; both are managed by ConfigSync.
	// Both exist in the repo and in the cluster.
	// Delete cm0 from the repo, expected a DependencyActuationMismatchError
	nt.T.Log("A new test: verify that removing a dependant from the git repo cause a dependency error")
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", k8sobjects.NamespaceObject(namespaceName)))
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Adding cm1 and cm0: cm1 depends on cm0"))
	nt.Must(nt.WatchForAllSyncs())
	nt.Must(rootSyncGitRepo.Remove("acme/cm0.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("Removing cm0 from the git repo"))
	nt.Must(nt.Watcher.WatchForRootSyncSyncError(configsync.RootSyncName, applier.ApplierErrorCode, "dependency", nil))

	// TestCase: cm1 depends on cm0; both are managed by ConfigSync.
	// Both exist in the repo and in the cluster.
	// Disable cm0,  expected an ExternalDependencyError
	nt.T.Log("A new test: verify that disabling a dependant from the git repo cause an external dependency error")
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Adding cm1 and cm0: cm1 depends on cm0"))
	nt.Must(nt.WatchForAllSyncs())
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName),
		metadata.WithManagementMode(metadata.ManagementDisabled))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Disabling management for cm0 in the git repo"))
	nt.Must(nt.Watcher.WatchForRootSyncSyncError(configsync.RootSyncName, applier.ApplierErrorCode, "external dependency", nil))

	// TestCase: cm1 depends on cm0; cm0 is disabled.
	// Neither exists in the cluster.
	// Expected an ExternalDependencyError
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Adding cm0 and cm1"))
	nt.Must(nt.WatchForAllSyncs())
	nt.Must(rootSyncGitRepo.Remove("acme/cm0.yaml"))
	nt.Must(rootSyncGitRepo.Remove("acme/cm1.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("Removing cm0 and cm1"))
	nt.Must(nt.WatchForAllSyncs())
	nt.T.Log("A new test: verify that disabling a dependant from the git repo cause an external dependency error")
	nt.Must(rootSyncGitRepo.Add("acme/cm0.yaml", k8sobjects.ConfigMapObject(core.Name(cm0Name), core.Namespace(namespaceName),
		metadata.WithManagementMode(metadata.ManagementDisabled))))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm0"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Adding cm1 and cm0: cm1 depends on cm0, cm0 is disabled"))
	nt.Must(nt.Watcher.WatchForRootSyncSyncError(configsync.RootSyncName, applier.ApplierErrorCode, "external dependency", nil))

	// TestCase: cm1 depends on object that is not in the repo or cluster.
	// Expected an ExternalDependencyError
	nt.T.Log("A new test: verify that a dependant not in the repo and not in the cluster cause an external dependency error")
	nt.Must(rootSyncGitRepo.Remove("acme/cm0.yaml"))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.CommitAndPush("cleaning cm0 and adding cm1"))
	nt.Must(nt.WatchForAllSyncs())
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName),
		core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm-not-exist"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Adding cm1: cm1 depends on a resource that doesn't exist in either the repo or in cluster"))
	nt.Must(nt.Watcher.WatchForRootSyncSyncError(configsync.RootSyncName, applier.ApplierErrorCode, "external dependency", nil))

	// TestCase: cm1 depends on an object that is not in the repo, but in the cluster
	// Expected an ExternalDependencyError
	nt.T.Log("A new test: verify that a dependant is only in the cluster cause an external dependency error")
	if _, err := nt.Shell.Kubectl("create", "configmap", "cm4", "-n", namespaceName); err != nil {
		nt.T.Fatal(err)
	}
	nt.T.Log("Verify that cm4 is created in the cluster")
	if err := nt.Validate("cm4", namespaceName, &corev1.ConfigMap{}); err != nil {
		nt.T.Fatal(err)
	}
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml",
		k8sobjects.ConfigMapObject(core.Name(cm1Name), core.Namespace(namespaceName),
			core.Annotation(dependson.Annotation, "/namespaces/bookstore/ConfigMap/cm4"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Adding cm1: cm1 depends on a resource that only exists in the cluster"))
	nt.Must(nt.Watcher.WatchForRootSyncSyncError(configsync.RootSyncName, applier.ApplierErrorCode, "external dependency", nil))
	nt.T.Log("Cleaning up")
	nt.Must(rootSyncGitRepo.Remove("acme/cm1.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("remove cm1"))
	nt.Must(nt.WatchForAllSyncs())
	if _, err := nt.Shell.Kubectl("delete", "configmap", "cm4", "-n", namespaceName); err != nil {
		nt.T.Fatal(err)
	}
	nt.T.Log("Verify that cm4 is deleted in the cluster")
	if err := nt.ValidateNotFound("cm4", namespaceName, &corev1.ConfigMap{}); err != nil {
		nt.T.Fatal(err)
	}
}

func TestDependencyWithReconciliation(t *testing.T) {
	// Increase reconcile timeout to account for slow pod scheduling due to cluster autoscaling.
	longTimeout := 5 * time.Minute
	nt := nomostest.New(t, nomostesting.Lifecycle,
		ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured),
		ntopts.WithReconcileTimeout(longTimeout))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	namespaceName := "bookstore"
	nt.T.Logf("Remove the namespace %q if it already exists", namespaceName)
	nt.MustKubectl("delete", "ns", namespaceName, "--ignore-not-found")

	// Add two pods in the namespace: pod1 and pod2, pod2 depends on pod1.
	nt.T.Log("add the namespace, pod1 and pod2, pod2 depends on pod1")
	pod1Name := "pod1"
	pod2Name := "pod2"
	container := corev1.Container{
		Name:  "goproxy",
		Image: "k8s.gcr.io/goproxy:0.1",
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 8080,
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(8080),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		},
	}
	nt.Must(rootSyncGitRepo.Add("acme/ns.yaml", k8sobjects.NamespaceObject(namespaceName)))
	nt.Must(rootSyncGitRepo.Add("acme/pod1.yaml",
		k8sobjects.PodObject(pod1Name, []corev1.Container{container}, core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.Add("acme/pod2.yaml",
		k8sobjects.PodObject(pod2Name, []corev1.Container{container}, core.Namespace(namespaceName),
			core.Annotation(dependson.Annotation, "/namespaces/bookstore/Pod/pod1"))))

	pod1 := &corev1.Pod{}
	pod2 := &corev1.Pod{}
	pod1SyncPredicate, pod1SyncCh := testpredicates.WatchSyncPredicate()
	pod2SyncPredicate, pod2SyncCh := testpredicates.WatchSyncPredicate()

	nt.T.Logf("Wait for both pods to become ready (background)")
	tg := taskgroup.New()
	tg.Go(func() error {
		return nt.Watcher.WatchObject(kinds.Pod(), pod1Name, namespaceName,
			testwatcher.WatchPredicates(
				pod1SyncPredicate,
				podCachePredicate(pod1),
				testpredicates.StatusEquals(nt.Scheme, kstatus.CurrentStatus),
			),
			testwatcher.WatchTimeout(nt.DefaultWaitTimeout*2))
	})
	tg.Go(func() error {
		return nt.Watcher.WatchObject(kinds.Pod(), pod2Name, namespaceName,
			testwatcher.WatchPredicates(
				pod2SyncPredicate,
				podCachePredicate(pod2),
				testpredicates.StatusEquals(nt.Scheme, kstatus.CurrentStatus),
			),
			testwatcher.WatchTimeout(nt.DefaultWaitTimeout*2))
	})
	// Watch in the background
	errCh := make(chan error)
	go func() {
		defer close(errCh)
		errCh <- tg.Wait()
	}()

	// Wait for both watches to be synchronized.
	// This means it knows the ResourceVersion from before the following changes.
	<-pod1SyncCh
	<-pod2SyncCh

	nt.Must(rootSyncGitRepo.CommitAndPush("Add pod1 and pod2 (pod2 depends on pod1)"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Logf("Wait for both pods to become ready (foreground)")
	if err := <-errCh; err != nil {
		nt.T.Fatal(err)
	}

	// Apply order: pod1 -> pod2 (pod2 depends on pod1)
	nt.T.Logf("Verify that pod2 was created after pod1 was ready")
	if pod2.CreationTimestamp.Before(getPodReadyTimestamp(pod1)) {
		nt.T.Fatalf("an object (%s) should be created after its dependency (%s) is ready",
			core.GKNN(pod2), core.GKNN(pod1))
	}

	nt.T.Logf("Remove Pod1 and Pod2")
	nt.Must(rootSyncGitRepo.Remove("acme/pod1.yaml"))
	nt.Must(rootSyncGitRepo.Remove("acme/pod2.yaml"))

	pod1 = &corev1.Pod{}
	pod2 = &corev1.Pod{}
	pod1SyncPredicate, pod1SyncCh = testpredicates.WatchSyncPredicate()
	pod2SyncPredicate, pod2SyncCh = testpredicates.WatchSyncPredicate()

	// Delete order: pod2 -> pod1 (pod2 depends on pod1)
	// Unfortunately, there's no "not found timestamp" to compare with the
	// deletion timestamp, so we're using `metadata.managedFields[*].time` as
	// a proxy for the "last update timestamp".
	// Note: We can't use client-side timestamps here, because the events are
	// not always received in the same order they happened, between two watches.
	var pod1DeletionTimestamp time.Time   // when pod1 is first MODIFIED to have a DeletionTimestamp
	var pod2LastUpdateTimestamp time.Time // when pod2 is last MODIFIED before being DELETED
	pod1DeletionPredicate := func(obj client.Object) error {
		if obj != nil && pod1DeletionTimestamp.IsZero() && !obj.GetDeletionTimestamp().IsZero() {
			pod1DeletionTimestamp = obj.GetDeletionTimestamp().Time
		}
		return nil
	}
	pod2LastUpdatedPredicate := func(obj client.Object) error {
		if obj != nil {
			lastUpdateTimestamp := getLastUpdateTimestamp(obj)
			if lastUpdateTimestamp.IsZero() {
				return errors.New("last update timestamp is missing for pod2")
			}
			pod2LastUpdateTimestamp = lastUpdateTimestamp.Time
		}
		return nil
	}

	nt.T.Logf("Wait for both pods to become not found (background)")
	tg = taskgroup.New()
	tg.Go(func() error {
		return nt.Watcher.WatchObject(kinds.Pod(), pod1Name, namespaceName,
			testwatcher.WatchPredicates(
				pod1SyncPredicate,
				podCachePredicate(pod1),
				pod1DeletionPredicate,
				testpredicates.ObjectNotFoundPredicate(nt.Scheme),
			),
			testwatcher.WatchTimeout(nt.DefaultWaitTimeout*2))
	})
	tg.Go(func() error {
		return nt.Watcher.WatchObject(kinds.Pod(), pod2Name, namespaceName,
			testwatcher.WatchPredicates(
				pod2SyncPredicate,
				podCachePredicate(pod2),
				pod2LastUpdatedPredicate,
				testpredicates.ObjectNotFoundPredicate(nt.Scheme),
			),
			testwatcher.WatchTimeout(nt.DefaultWaitTimeout*2))
	})

	// Watch in the background
	errCh = make(chan error)
	go func() {
		defer close(errCh)
		errCh <- tg.Wait()
	}()

	// Wait for both watches to be synchronized.
	// This means it knows the ResourceVersion from before the following changes.
	<-pod1SyncCh
	<-pod2SyncCh

	nt.Must(rootSyncGitRepo.CommitAndPush("Remove pod1 and pod2"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Logf("Wait for both pods to become not found (foreground)")
	if err := <-errCh; err != nil {
		nt.T.Fatal(err)
	}

	// Delete order: pod2 -> pod1 (pod2 depends on pod1)
	nt.T.Logf("Verify that pod1 was deleted after pod2 was not found")
	if !pod1DeletionTimestamp.After(pod2LastUpdateTimestamp) {
		nt.T.Logf("pod2 last update timestamp: %s", pod2LastUpdateTimestamp.Format(time.RFC3339Nano))
		nt.T.Logf("pod1 deletion timestamp: %s", pod1DeletionTimestamp.Format(time.RFC3339Nano))
		nt.T.Fatalf("an object (%s) should be deleted after its dependency (%s) is not found",
			core.GKNN(pod1), core.GKNN(pod2))
	}

	nt.T.Log("add pod3 and pod4, pod3's image is not valid, pod4 depends on pod3")
	rs := k8sobjects.RootSyncObjectV1Beta1(nomostest.DefaultRootSyncID.Name)
	nt.T.Log("Set a shorter reconcileTimeout, since the scenario is expected to not reconcile")
	nt.Must(nt.KubeClient.MergePatch(rs, `{"spec": {"override": {"reconcileTimeout": "30s"}}}`))
	invalidImageContainer := container
	invalidImageContainer.Image = "does-not-exist"
	nt.Must(rootSyncGitRepo.Add("acme/pod3.yaml",
		k8sobjects.PodObject("pod3", []corev1.Container{invalidImageContainer}, core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.Add("acme/pod4.yaml",
		k8sobjects.PodObject("pod4", []corev1.Container{container}, core.Namespace(namespaceName),
			core.Annotation(dependson.Annotation, "/namespaces/bookstore/Pod/pod3"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Add pod3 and pod4 (pod4 depends on pod3 and pod3 won't be reconciled)"))
	nt.Must(nt.Watcher.WatchForRootSyncSyncError(configsync.RootSyncName, applier.ApplierErrorCode,
		"skipped apply of Pod, bookstore/pod4: dependency apply reconcile timeout: bookstore_pod3__Pod", nil))

	nt.T.Logf("Verify that pod3 is created but not ready and pod4 is not found")
	var err error
	// pod3 will never reconcile (image pull failure)
	// TODO: kstatus should probably detect image pull failure and time out to Failure status, like it does for scheduling failure.
	err = multierr.Append(err, nt.Watcher.WatchObject(kinds.Pod(), "pod3", namespaceName,
		testwatcher.WatchPredicates(testpredicates.StatusEquals(nt.Scheme, kstatus.InProgressStatus))))
	err = multierr.Append(err, nt.ValidateNotFound("pod4", namespaceName, &corev1.Pod{}))
	if err != nil {
		nt.T.Fatal(err)
	}
	nt.T.Log("Restore the longer reconcileTimeout override")
	nt.Must(nt.KubeClient.MergePatch(rs,
		fmt.Sprintf(`{"spec": {"override": {"reconcileTimeout": "%s"}}}`, longTimeout)))

	nt.T.Log("cleanup")
	nt.Must(rootSyncGitRepo.Remove("acme/pod3.yaml"))
	nt.Must(rootSyncGitRepo.Remove("acme/pod4.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("Remove pod3 and pod4"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Logf("Verify that pod3 and pod4 are not found")
	err = multierr.Append(err, nt.ValidateNotFound("pod3", namespaceName, &corev1.Pod{}))
	err = multierr.Append(err, nt.ValidateNotFound("pod4", namespaceName, &corev1.Pod{}))
	if err != nil {
		nt.T.Fatal(err)
	}

	// TestCase: pod6 depends on pod5; both are managed by ConfigSync.
	// Both exist in the repo and in the cluster.
	// Delete pod5 from the repo.
	// pod5 shouldn't be pruned in the cluster since pod6 depends on it.
	nt.T.Log("add pod5 and pod6, pod6 depends on pod5. Delete pod5 from the repo. pruning pod5 should be skipped.")
	nt.T.Logf("Add Pod5 and Pod6")
	nt.Must(rootSyncGitRepo.Add("acme/pod5.yaml",
		k8sobjects.PodObject("pod5", []corev1.Container{container}, core.Namespace(namespaceName))))
	nt.Must(rootSyncGitRepo.Add("acme/pod6.yaml",
		k8sobjects.PodObject("pod6", []corev1.Container{container}, core.Namespace(namespaceName),
			core.Annotation(dependson.Annotation, "/namespaces/bookstore/Pod/pod5"))))
	nt.Must(rootSyncGitRepo.CommitAndPush("Add pod5 and pod6"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Logf("Verify that pod5 and pod6 are ready")
	err = multierr.Append(err, nt.Validate("pod5", namespaceName, &corev1.Pod{},
		testpredicates.StatusEquals(nt.Scheme, kstatus.CurrentStatus)))
	err = multierr.Append(err, nt.Validate("pod5", namespaceName, &corev1.Pod{},
		testpredicates.StatusEquals(nt.Scheme, kstatus.CurrentStatus)))
	if err != nil {
		nt.T.Fatal(err)
	}

	nt.T.Logf("Remove pod5 from the repo")
	nt.Must(rootSyncGitRepo.Remove("acme/pod5.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("Remove pod5"))
	nt.Must(nt.Watcher.WatchForRootSyncSyncError(configsync.RootSyncName, applier.ApplierErrorCode, "dependency", nil))

	nt.T.Logf("Verify that pod5 and pod6 were not deleted")
	err = multierr.Append(err, nt.Validate("pod5", namespaceName, &corev1.Pod{},
		testpredicates.MissingDeletionTimestamp))
	err = multierr.Append(err, nt.Validate("pod6", namespaceName, &corev1.Pod{},
		testpredicates.MissingDeletionTimestamp))
	if err != nil {
		nt.T.Fatal(err)
	}

	nt.T.Logf("Remove Pod6")
	nt.Must(rootSyncGitRepo.Remove("acme/pod6.yaml"))
	nt.Must(rootSyncGitRepo.CommitAndPush("Remove pod6"))
	nt.Must(nt.WatchForAllSyncs())

	nt.T.Logf("Verify that pod5 and pod6 were deleted")
	err = multierr.Append(err, nt.ValidateNotFound("pod5", namespaceName, &corev1.Pod{}))
	err = multierr.Append(err, nt.ValidateNotFound("pod6", namespaceName, &corev1.Pod{}))
	if err != nil {
		nt.T.Fatal(err)
	}
}

func verifyManagedBy(nt *nomostest.NT, o client.Object, rsync core.ID) error {
	return nt.Validate(o.GetName(), o.GetNamespace(), o,
		testpredicates.IsManagedBy(nt.Scheme, declared.ScopeFromSyncNamespace(rsync.Namespace), rsync.Name))
}

func verifyUnmanaged(nt *nomostest.NT, o client.Object) error {
	return nt.Validate(o.GetName(), o.GetNamespace(), o,
		testpredicates.NoConfigSyncMetadata())
}

func TestSplitRSyncsWithDeletion(t *testing.T) {
	ns1 := func() *corev1.Namespace {
		return k8sobjects.NamespaceObject("ns1")
	}
	anvilCRD := func() *apiextensionsv1.CustomResourceDefinition {
		return anvilV1CRD()
	}
	anvilCR := func() *unstructured.Unstructured {
		o := newAnvilObject("v1", "anvil", 42)
		o.SetNamespace("foo")
		return o
	}
	tenantNS1 := frontendNamespace
	tenantNS2 := backendNamespace
	cm1 := func() *corev1.ConfigMap {
		return k8sobjects.ConfigMapObject(core.Name("cm1"), core.Namespace(tenantNS1))
	}
	sa1 := func() *corev1.ServiceAccount {
		return k8sobjects.ServiceAccountObject("sa1", core.Namespace(tenantNS2))
	}
	rootSync := nomostest.DefaultRootSyncID
	rootSync1 := core.RootSyncID("sync-x")
	rootSync2 := core.RootSyncID("sync-y")
	repoSync1 := core.RepoSyncID(configsync.RepoSyncName, tenantNS1)
	repoSync2 := core.RepoSyncID(configsync.RepoSyncName, tenantNS2)
	nt := nomostest.New(t, nomostesting.Lifecycle,
		ntopts.SyncWithGitSource(rootSync, ntopts.Unstructured),
		ntopts.SyncWithGitSource(rootSync1, ntopts.Unstructured),
		ntopts.SyncWithGitSource(rootSync2, ntopts.Unstructured),
		ntopts.SyncWithGitSource(repoSync1, ntopts.Unstructured),
		ntopts.SyncWithGitSource(repoSync2, ntopts.Unstructured),
		ntopts.WithDelegatedControl,
		ntopts.WithoutDeletionPropagationPolicy(),
		ntopts.RepoSyncPermissions(policy.CoreAdmin()))

	nt.T.Log("Add all resources to a single RootSync")
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(rootSync)
	nt.Must(rootSyncGitRepo.Add("acme/ns1.yaml", ns1()))
	nt.Must(rootSyncGitRepo.Add("acme/anvil_crd.yaml", anvilCRD()))
	nt.Must(rootSyncGitRepo.Add("acme/anvil_cr.yaml", anvilCR()))
	nt.Must(rootSyncGitRepo.Add("acme/cm1.yaml", cm1()))
	nt.Must(rootSyncGitRepo.Add("acme/sa1.yaml", sa1()))
	nt.Must(rootSyncGitRepo.CommitAndPush("Add objects"))
	nt.Must(nt.WatchForAllSyncs())
	// Validate metadata for each object
	nt.Must(verifyManagedBy(nt, ns1(), rootSync))
	nt.Must(verifyManagedBy(nt, anvilCRD(), rootSync))
	nt.Must(verifyManagedBy(nt, anvilCR(), rootSync))
	nt.Must(verifyManagedBy(nt, cm1(), rootSync))
	nt.Must(verifyManagedBy(nt, sa1(), rootSync))

	nt.T.Cleanup(func() {
		rs := nomostest.RootSyncObjectV1Beta1FromRootRepo(nt, rootSync.Name)
		nt.Must(nt.KubeClient.Apply(rs))
		nt.Must(nt.WatchForAllSyncs())
	})
	nt.T.Log("Delete the single RootSync to orphan all resources")
	nt.Must(nt.KubeClient.Delete(k8sobjects.RootSyncV1Beta1(rootSync.Name)))
	nt.Must(nt.Watcher.WatchForNotFound(kinds.RootSyncV1Beta1(), rootSync.Name, rootSync.Namespace))
	nt.Must(verifyUnmanaged(nt, ns1()))
	nt.Must(verifyUnmanaged(nt, anvilCRD()))
	nt.Must(verifyUnmanaged(nt, anvilCR()))
	nt.Must(verifyUnmanaged(nt, cm1()))
	nt.Must(verifyUnmanaged(nt, sa1()))

	nt.T.Cleanup(func() {
		// Remove objects from the original RSync for cleanup
		repo := nt.SyncSourceGitReadWriteRepository(rootSync)
		nt.Must(repo.Remove("acme/ns1.yaml"))
		nt.Must(repo.Remove("acme/anvil_crd.yaml"))
		nt.Must(repo.Remove("acme/anvil_cr.yaml"))
		nt.Must(repo.Remove("acme/cm1.yaml"))
		nt.Must(repo.Remove("acme/sa1.yaml"))
		nt.Must(repo.CommitAndPush("Remove orphaned objects"))
	})
	nt.T.Log("Adopt each resource by various RootSyncs/RepoSyncs")
	repo := nt.SyncSourceGitReadWriteRepository(rootSync1)
	nt.Must(repo.Add("acme/ns1.yaml", ns1()))
	nt.Must(repo.CommitAndPush("Take ownership of ns1 in root-sync-1"))
	repo = nt.SyncSourceGitReadWriteRepository(rootSync2)
	nt.Must(repo.Add("acme/anvil_crd.yaml", anvilCRD()))
	nt.Must(repo.Add("acme/anvil_cr.yaml", anvilCR()))
	nt.Must(repo.CommitAndPush("Take ownership of anvil in root-sync-2"))
	repo = nt.SyncSourceGitReadWriteRepository(repoSync1)
	nt.Must(repo.Add("acme/cm1.yaml", cm1()))
	nt.Must(repo.CommitAndPush("Take ownership of cm1 in repo-sync-1"))
	repo = nt.SyncSourceGitReadWriteRepository(repoSync2)
	nt.Must(repo.Add("acme/sa1.yaml", sa1()))
	nt.Must(repo.CommitAndPush("Take ownership of sa1 in repo-sync-2"))
	// Ready check recreates the root-sync, which we don't want
	nt.Must(nt.WatchForAllSyncs(nomostest.SkipReadyCheck(), nomostest.SkipRootRepos(rootSync.Name)))
	nt.Must(verifyManagedBy(nt, ns1(), rootSync1))
	nt.Must(verifyManagedBy(nt, anvilCRD(), rootSync2))
	nt.Must(verifyManagedBy(nt, anvilCR(), rootSync2))
	nt.Must(verifyManagedBy(nt, cm1(), repoSync1))
	nt.Must(verifyManagedBy(nt, sa1(), repoSync2))
}

func getPodReadyTimestamp(pod *corev1.Pod) *metav1.Time {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.LastTransitionTime.DeepCopy()
		}
	}
	return nil
}

// getLastUpdateTimestamp uses the newest `metadata.managedFields[*].time`
// value as a proxy for the time when the object was last updated.
func getLastUpdateTimestamp(obj client.Object) *metav1.Time {
	var lastUpdateTimestamp *metav1.Time
	for _, entry := range obj.GetManagedFields() {
		if !entry.Time.IsZero() {
			if lastUpdateTimestamp.IsZero() || entry.Time.Time.After(lastUpdateTimestamp.Time) {
				lastUpdateTimestamp = entry.Time.DeepCopy()
			}
		}
	}
	return lastUpdateTimestamp
}

// podCachePredicate returns a predicate which overwrites the specified pod with
// the latest pod, every time the predicate is called with a non-nil object.
// This can be used to export the last known pod state during a watch or wait.
func podCachePredicate(pod *corev1.Pod) testpredicates.Predicate {
	return func(obj client.Object) error {
		if obj != nil {
			latestPod, ok := obj.(*corev1.Pod)
			if !ok {
				return testpredicates.WrongTypeErr(obj, &corev1.Pod{})
			}
			latestPod.DeepCopyInto(pod)
		}
		return nil
	}
}
