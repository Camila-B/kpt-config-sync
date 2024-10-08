// Copyright 2023 Google LLC
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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"kpt.dev/configsync/e2e/nomostest"
	"kpt.dev/configsync/e2e/nomostest/ntopts"
	"kpt.dev/configsync/e2e/nomostest/policy"
	nomostesting "kpt.dev/configsync/e2e/nomostest/testing"
	"kpt.dev/configsync/e2e/nomostest/testpredicates"
	"kpt.dev/configsync/e2e/nomostest/testwatcher"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/core/k8sobjects"
	"kpt.dev/configsync/pkg/kinds"
	"kpt.dev/configsync/pkg/reconcilermanager"
	"kpt.dev/configsync/pkg/reconcilermanager/controllers"
	"kpt.dev/configsync/pkg/status"
)

func TestRootSyncSSHKnownHost(t *testing.T) {
	nt := nomostest.New(t, nomostesting.SyncSource, ntopts.RequireLocalGitProvider,
		ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured))
	rootSyncGitRepo := nt.SyncSourceGitReadWriteRepository(nomostest.DefaultRootSyncID)

	var err error
	rootSecret := &corev1.Secret{}
	nt.T.Cleanup(func() {
		nt.MustMergePatch(rootSecret, secretDataDeletePatch(controllers.KnownHostsKey))
		err = nt.Watcher.WatchObject(kinds.Deployment(),
			core.RootReconcilerPrefix, configsync.ControllerNamespace,
			testwatcher.WatchPredicates(
				testpredicates.DeploymentHasEnvVar(reconcilermanager.GitSync, controllers.GitSyncKnownHosts, "false"),
			),
		)
		if err != nil {
			nt.T.Error(err)
		}
	})

	// validate secret and deployment initial stage
	err = nt.Validate(nomostest.RootAuthSecretName, configsync.ControllerNamespace, rootSecret)
	if err != nil {
		nt.T.Fatal(err)
	}
	nt.Must(nt.Watcher.WatchObject(kinds.Deployment(),
		core.RootReconcilerPrefix, configsync.ControllerNamespace,
		testwatcher.WatchPredicates(
			testpredicates.DeploymentHasEnvVar(reconcilermanager.GitSync, controllers.GitSyncKnownHosts, "false"),
		),
	))

	// get known host key value
	knownHostValue, err := nomostest.GetKnownHosts(nt)
	if err != nil {
		nt.T.Fatalf("error: %s", err)
	}

	// apply known host key and validate
	nt.MustMergePatch(rootSecret, secretDataPatch(controllers.KnownHostsKey, knownHostValue))
	nt.Must(nt.Watcher.WatchObject(kinds.Deployment(),
		core.RootReconcilerPrefix, configsync.ControllerNamespace,
		testwatcher.WatchPredicates(
			testpredicates.DeploymentHasEnvVar(reconcilermanager.GitSync, controllers.GitSyncKnownHosts, "true"),
		),
	))

	// try syncing resource and validate
	cmName := "configmap-test"
	cmPath := "acme/configmap.yaml"
	cm := k8sobjects.ConfigMapObject(core.Name(cmName))
	nt.Must(rootSyncGitRepo.Add(cmPath, cm))
	nt.Must(rootSyncGitRepo.CommitAndPush("Adding test ConfigMap"))
	nt.Must(nt.WatchForAllSyncs())
	err = nt.Validate(cmName, "default", &corev1.ConfigMap{})
	if err != nil {
		nt.T.Fatal(err)
	}
	nt.Must(rootSyncGitRepo.Remove(cmPath))
	nt.Must(rootSyncGitRepo.CommitAndPush("Removing test ConfigMap"))
	nt.Must(nt.WatchForAllSyncs())
	if err := nt.ValidateNotFound(cmName, "default", &corev1.ConfigMap{}); err != nil {
		nt.T.Fatalf("error: %s", err)
	}

	// validate root sync error using invalid known host value
	knownHostValue = "invalid value"
	nt.MustMergePatch(rootSecret, secretDataPatch(controllers.KnownHostsKey, knownHostValue))
	nt.Must(nt.Watcher.WatchObject(kinds.Deployment(),
		core.RootReconcilerPrefix, configsync.ControllerNamespace,
		testwatcher.WatchPredicates(
			testpredicates.DeploymentHasEnvVar(reconcilermanager.GitSync, controllers.GitSyncKnownHosts, "true"),
		),
	))
	nt.Must(nt.Watcher.WatchObject(kinds.RootSyncV1Beta1(), configsync.RootSyncName, configsync.ControllerNamespace,
		testwatcher.WatchPredicates(
			testpredicates.RootSyncHasSourceError(status.SourceErrorCode, "No ED25519 host key is known"),
		)))
}

func TestRepoSyncSSHKnownHost(t *testing.T) {
	repoSyncID := core.RepoSyncID(configsync.RepoSyncName, backendNamespace)
	nt := nomostest.New(t,
		nomostesting.SyncSource, ntopts.RequireLocalGitProvider,
		ntopts.WithDelegatedControl,
		ntopts.SyncWithGitSource(nomostest.DefaultRootSyncID, ntopts.Unstructured),
		ntopts.SyncWithGitSource(repoSyncID),
		ntopts.RepoSyncPermissions(policy.AppsAdmin(), policy.CoreAdmin()))
	repoSyncGitRepo := nt.SyncSourceGitReadWriteRepository(repoSyncID)

	var err error
	repoSecret := &corev1.Secret{}
	repoReconcilerName := core.NsReconcilerName(repoSyncID.Namespace, repoSyncID.Name)
	nt.T.Cleanup(func() {
		nt.MustMergePatch(repoSecret, secretDataDeletePatch(controllers.KnownHostsKey))
		err = nt.Watcher.WatchObject(kinds.Deployment(),
			repoReconcilerName, configsync.ControllerNamespace,
			testwatcher.WatchPredicates(
				testpredicates.DeploymentHasEnvVar(reconcilermanager.GitSync, controllers.GitSyncKnownHosts, "false"),
			),
		)
		if err != nil {
			nt.T.Error(err)
		}
	})

	// validate secret and deployment initial stage
	err = nt.Validate(nomostest.NamespaceAuthSecretName, backendNamespace, repoSecret)
	if err != nil {
		nt.T.Fatal(err)
	}
	nt.Must(nt.Watcher.WatchObject(kinds.Deployment(),
		repoReconcilerName, configsync.ControllerNamespace,
		testwatcher.WatchPredicates(
			testpredicates.DeploymentHasEnvVar(reconcilermanager.GitSync, controllers.GitSyncKnownHosts, "false"),
		),
	))

	// get known host key value
	knownHostValue, err := nomostest.GetKnownHosts(nt)
	if err != nil {
		nt.T.Fatalf("error: %s", err)
	}

	// apply known host key and validate
	nt.MustMergePatch(repoSecret, secretDataPatch(controllers.KnownHostsKey, knownHostValue))
	nt.Must(nt.Watcher.WatchObject(kinds.Deployment(),
		repoReconcilerName, configsync.ControllerNamespace,
		testwatcher.WatchPredicates(
			testpredicates.DeploymentHasEnvVar(reconcilermanager.GitSync, controllers.GitSyncKnownHosts, "true"),
		),
	))

	// try syncing resource and validate
	cmName := "configmap-test"
	cmPath := "acme/configmap.yaml"
	cm := k8sobjects.ConfigMapObject(core.Name(cmName))
	nt.Must(repoSyncGitRepo.Add(cmPath, cm))
	nt.Must(repoSyncGitRepo.CommitAndPush("Adding test ConfigMap"))
	nt.Must(nt.WatchForAllSyncs())
	err = nt.Validate(cmName, backendNamespace, &corev1.ConfigMap{})
	if err != nil {
		nt.T.Fatal(err)
	}
	nt.Must(repoSyncGitRepo.Remove(cmPath))
	nt.Must(repoSyncGitRepo.CommitAndPush("Removing test ConfigMap"))
	nt.Must(nt.WatchForAllSyncs())
	if err := nt.ValidateNotFound(cmName, backendNamespace, &corev1.ConfigMap{}); err != nil {
		nt.T.Fatalf("error: %s", err)
	}

	// validate repo sync error using invalid known host value
	knownHostValue = "invalid value"
	nt.MustMergePatch(repoSecret, secretDataPatch(controllers.KnownHostsKey, knownHostValue))
	nt.Must(nt.Watcher.WatchObject(kinds.Deployment(),
		repoReconcilerName, configsync.ControllerNamespace,
		testwatcher.WatchPredicates(
			testpredicates.DeploymentHasEnvVar(reconcilermanager.GitSync, controllers.GitSyncKnownHosts, "true"),
		),
	))
	nt.Must(nt.Watcher.WatchObject(kinds.RepoSyncV1Beta1(), configsync.RepoSyncName, backendNamespace,
		testwatcher.WatchPredicates(
			testpredicates.RepoSyncHasSourceError(status.SourceErrorCode, "No ED25519 host key is known"),
		)))
}
