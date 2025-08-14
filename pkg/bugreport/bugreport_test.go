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

package bugreport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"kpt.dev/configsync/cmd/nomos/util"
	"kpt.dev/configsync/pkg/api/configmanagement"
	configmanagementv1 "kpt.dev/configsync/pkg/api/configmanagement/v1"
	"kpt.dev/configsync/pkg/api/configsync"
	configsyncv1beta1 "kpt.dev/configsync/pkg/api/configsync/v1beta1"
	kptv1alpha1 "kpt.dev/configsync/pkg/api/kpt.dev/v1alpha1"
	"kpt.dev/configsync/pkg/client/restconfig"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/core/k8sobjects"
	"kpt.dev/configsync/pkg/metadata"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestAssembleLogSources(t *testing.T) {
	tests := []struct {
		name           string
		ns             v1.Namespace
		pods           v1.PodList
		expectedValues logSources
	}{
		{
			name:           "No pods",
			ns:             *k8sobjects.NamespaceObject("foo"),
			pods:           v1.PodList{Items: make([]v1.Pod, 0)},
			expectedValues: make(logSources, 0),
		},
		{
			name: "Multiple pods with various container configurations",
			ns:   *k8sobjects.NamespaceObject("foo"),
			pods: v1.PodList{Items: []v1.Pod{
				*k8sobjects.PodObject("foo_a", []v1.Container{
					*k8sobjects.ContainerObject("1"),
					*k8sobjects.ContainerObject("2"),
				}),
				*k8sobjects.PodObject("foo_b", []v1.Container{
					*k8sobjects.ContainerObject("3"),
				}),
			}},
			expectedValues: logSources{
				&logSource{
					ns: *k8sobjects.NamespaceObject("foo"),
					pod: *k8sobjects.PodObject("foo_a", []v1.Container{
						*k8sobjects.ContainerObject("1"),
						*k8sobjects.ContainerObject("2"),
					}),
					cont: *k8sobjects.ContainerObject("1"),
				},
				&logSource{
					ns: *k8sobjects.NamespaceObject("foo"),
					pod: *k8sobjects.PodObject("foo_a", []v1.Container{
						*k8sobjects.ContainerObject("1"),
						*k8sobjects.ContainerObject("2"),
					}),
					cont: *k8sobjects.ContainerObject("2"),
				},
				&logSource{
					ns: *k8sobjects.NamespaceObject("foo"),
					pod: *k8sobjects.PodObject("foo_b", []v1.Container{
						*k8sobjects.ContainerObject("3"),
					}),
					cont: *k8sobjects.ContainerObject("3"),
				},
			},
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			outputs := assembleLogSources(test.ns, test.pods)

			sort.Sort(outputs)
			sort.Sort(test.expectedValues)

			for i, output := range outputs {
				expected := test.expectedValues[i]
				if diff := cmp.Diff(output, expected, cmp.AllowUnexported(logSource{})); diff != "" {
					t.Errorf("%T differ (-got, +want): %s", expected, diff)
				}
			}
		})
	}
}

func TestNewBugReporter(t *testing.T) {
	rmObj := k8sobjects.DeploymentObject(core.Namespace(configsync.ControllerNamespace), core.Name(util.ReconcilerManagerName))

	tests := []struct {
		name              string
		k8sContext        string
		contextErr        error
		client            client.Client
		configSyncEnabled bool
		wantErrorList     []error
	}{
		{
			name:              "success",
			k8sContext:        "test-context",
			client:            fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithObjects(rmObj).Build(),
			configSyncEnabled: true,
		},
		{
			name:              "reconciler-manager deployment not found",
			k8sContext:        "test-context",
			client:            fakeclient.NewClientBuilder().WithScheme(core.Scheme).Build(),
			configSyncEnabled: false,
		},
		{
			name:       "generic error on Get",
			k8sContext: "test-context",
			client: fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithInterceptorFuncs(
				interceptor.Funcs{
					Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
						return apierrors.NewInternalError(errors.New("generic Get error"))
					},
				}).Build(),
			wantErrorList: []error{apierrors.NewInternalError(errors.New("generic Get error"))},
		},
		{
			name:          "error getting k8s context",
			k8sContext:    "",
			contextErr:    errors.New("context error"),
			client:        fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithObjects(rmObj).Build(),
			wantErrorList: []error{errors.New("context error")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restconfig.CurrentContextName = func() (string, error) {
				return tt.k8sContext, tt.contextErr
			}
			cs := fake.NewClientset()

			br, err := New(context.Background(), tt.client, cs)
			if err != nil {
				t.Fatalf("New() returned an unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.k8sContext, br.k8sContext); diff != "" {
				t.Errorf("BugReporter.k8sContext diff (-want +got):\n%s", diff)
			}

			if len(tt.wantErrorList) != len(br.ErrorList) {
				t.Errorf("len(ErrorList) got %d, want %d. Got errors: %v, want errors: %v", len(br.ErrorList), len(tt.wantErrorList), br.ErrorList, tt.wantErrorList)
			} else {
				for i, wantErr := range tt.wantErrorList {
					if br.ErrorList[i].Error() != wantErr.Error() {
						t.Errorf("ErrorList[%d] got %q, want %q", i, br.ErrorList[i], wantErr)
					}
				}
			}
		})
	}
}

func TestFetchLogSources(t *testing.T) {
	csNS := k8sobjects.NamespaceObject(configsync.ControllerNamespace, core.Label(metadata.SystemLabel, "true"))
	csPod := k8sobjects.PodObject("cs-pod", []v1.Container{*k8sobjects.ContainerObject("cs-cont")}, core.Namespace(csNS.Name))
	csPodMultiCont := k8sobjects.PodObject("cs-pod-multi", []v1.Container{
		*k8sobjects.ContainerObject("cs-cont-1"),
		*k8sobjects.ContainerObject("cs-cont-2"),
	}, core.Namespace(csNS.Name))

	monitoringNS := k8sobjects.NamespaceObject(configmanagement.MonitoringNamespace)
	monitoringPod := k8sobjects.PodObject("mon-pod", []v1.Container{*k8sobjects.ContainerObject("mon-cont")}, core.Namespace(monitoringNS.Name))

	rgNS := k8sobjects.NamespaceObject(configmanagement.RGControllerNamespace, core.Label(metadata.SystemLabel, "true"))
	rgPod := k8sobjects.PodObject("rg-pod", []v1.Container{*k8sobjects.ContainerObject("rg-cont")}, core.Namespace(rgNS.Name))

	rmObj := k8sobjects.DeploymentObject(core.Namespace(configsync.ControllerNamespace), core.Name(util.ReconcilerManagerName))

	tests := []struct {
		name              string
		client            client.Client
		wantReadableNames []string
		wantErrCount      int
	}{
		{
			name:   "all components enabled",
			client: fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithObjects(csNS, csPod, monitoringNS, monitoringPod, rgNS, rgPod, rmObj).Build(),
			wantReadableNames: []string{
				"namespaces/config-management-monitoring/mon-pod/mon-cont.log",
				"namespaces/config-management-system/cs-pod/cs-cont.log",
				"namespaces/resource-group-system/rg-pod/rg-cont.log",
			},
		},
		{
			name:   "only config sync enabled, others missing",
			client: fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithObjects(csNS, csPod, rmObj).Build(),

			wantReadableNames: []string{
				"namespaces/config-management-system/cs-pod/cs-cont.log",
			},
			wantErrCount: 2, // for missing monitoring and rg namespaces
		},
		{
			name:         "no components enabled",
			client:       fakeclient.NewClientBuilder().WithScheme(core.Scheme).Build(),
			wantErrCount: 4,
		},
		{
			name: "log fetch error",
			client: fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithInterceptorFuncs(
				interceptor.Funcs{
					Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
						return apierrors.NewInternalError(errors.New("generic Get error"))
					},
				}).WithObjects(csNS, csPod, monitoringNS, monitoringPod, rgNS, rgPod).Build(),

			wantErrCount: 4,
		},
		{
			name:   "pod with multiple containers",
			client: fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithObjects(csNS, csPodMultiCont, rmObj).Build(),
			wantReadableNames: []string{
				"namespaces/config-management-system/cs-pod-multi/cs-cont-1.log",
				"namespaces/config-management-system/cs-pod-multi/cs-cont-2.log",
			},
			wantErrCount: 2, // for missing monitoring and rg namespaces
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br, err := New(context.Background(), tt.client, fake.NewClientset())
			if err != nil {
				t.Fatalf("New() returned an unexpected error: %v", err)
			}
			readables := br.FetchLogSources(context.Background())

			if len(br.ErrorList) != tt.wantErrCount {
				t.Errorf("got %d errors, want %d. Errors: %v", len(br.ErrorList), tt.wantErrCount, br.ErrorList)
			}

			var readableNames []string
			for _, r := range readables {
				readableNames = append(readableNames, r.Name)
			}
			sort.Strings(readableNames)
			sort.Strings(tt.wantReadableNames)

			if diff := cmp.Diff(tt.wantReadableNames, readableNames); diff != "" {
				t.Errorf("FetchLogSources() returned unexpected readables (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFetchResources(t *testing.T) {
	csObj := &configsyncv1beta1.RootSync{
		ObjectMeta: metav1.ObjectMeta{Name: "root-sync", Namespace: "config-management-system"},
	}
	rgObj := &kptv1alpha1.ResourceGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rg", Namespace: "default"},
	}
	cm := k8sobjects.ConfigMapObject(core.Name("foo"), core.Namespace(configmanagement.ControllerNamespace))
	webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "admission-webhook.configsync.gke.io",
		},
	}
	rmObj := k8sobjects.DeploymentObject(core.Namespace(configsync.ControllerNamespace), core.Name(util.ReconcilerManagerName))

	tests := []struct {
		name              string
		client            client.Client
		clientSet         kubernetes.Interface
		wantReadableNames []string
		wantErrCount      int
	}{
		{
			name:   "all resources found",
			client: fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithObjects(csObj, rgObj, rmObj).Build(),
			clientSet: func() kubernetes.Interface {
				cs := fakeclientset.NewClientset(cm, webhook, rmObj)
				fakeDiscovery, ok := cs.Discovery().(*fakediscovery.FakeDiscovery)
				if !ok {
					panic("couldn't convert Discovery() to *FakeDiscovery")
				}
				fakeDiscovery.Resources = []*metav1.APIResourceList{
					{
						GroupVersion: configmanagementv1.SchemeGroupVersion.String(),
						APIResources: []metav1.APIResource{
							{Name: "configmanagements", SingularName: "configmanagement", Kind: "ConfigManagementList"},
						},
					},
					{
						GroupVersion: configsyncv1beta1.SchemeGroupVersion.String(),
						APIResources: []metav1.APIResource{
							{Name: "rootsyncs", SingularName: "rootsync", Kind: "RootSyncList", Namespaced: true},
						},
					},
					{
						GroupVersion: kptv1alpha1.SchemeGroupVersionKind().GroupVersion().String(),
						APIResources: []metav1.APIResource{
							{Name: "resourcegroups", SingularName: "resourcegroup", Kind: "ResourceGroupList", Namespaced: true},
						},
					},
					{
						GroupVersion: appsv1.SchemeGroupVersion.String(),
						APIResources: []metav1.APIResource{
							{Name: "deployments", SingularName: "deployment", Kind: "DeploymentList", Namespaced: true},
						},
					},
				}
				return cs
			}(),
			wantReadableNames: []string{
				"cluster/configmanagement/config-sync-validating-webhhook-configuration.yaml",
				"cluster/configmanagement/configmanagements.yaml",
				"namespaces/config-management-monitoring/ConfigMaps.yaml",
				"namespaces/config-management-system/ConfigMaps.yaml",
				"namespaces/config-management-system/RootSync-root-sync.yaml",
				"namespaces/default/ResourceGroup-rg.yaml",
				"namespaces/resource-group-system/ConfigMaps.yaml",
			},
		},
		{
			name: "list error",
			client: fakeclient.NewClientBuilder().WithScheme(core.Scheme).WithObjects(rmObj).WithInterceptorFuncs(interceptor.Funcs{
				List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
					return apierrors.NewInternalError(errors.New("generic List error"))
				},
			}).Build(),
			clientSet: func() kubernetes.Interface {
				cs := fakeclientset.NewClientset(cm, webhook)
				cs.PrependReactor("list", "*", func(_ clienttesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, apierrors.NewInternalError(errors.New("generic List error clientset"))
				})
				cs.PrependReactor("get", "*", func(_ clienttesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, apierrors.NewInternalError(errors.New("generic List error clientset"))
				})
				fakeDiscovery, ok := cs.Discovery().(*fakediscovery.FakeDiscovery)
				if !ok {
					panic("couldn't convert Discovery() to *FakeDiscovery")
				}
				fakeDiscovery.Resources = []*metav1.APIResourceList{
					{
						GroupVersion: configmanagementv1.SchemeGroupVersion.String(),
						APIResources: []metav1.APIResource{
							{Name: "configmanagements", SingularName: "configmanagement", Kind: "ConfigManagement"},
						},
					},
					{
						GroupVersion: configsyncv1beta1.SchemeGroupVersion.String(),
						APIResources: []metav1.APIResource{
							{Name: "rootsyncs", SingularName: "rootsync", Kind: "RootSync", Namespaced: true},
						},
					},
					{
						GroupVersion: kptv1alpha1.SchemeGroupVersionKind().GroupVersion().String(),
						APIResources: []metav1.APIResource{
							{Name: "resourcegroups", SingularName: "resourcegroup", Kind: "ResourceGroup", Namespaced: true},
						},
					},
				}
				return cs
			}(),

			wantErrCount: 7, // for cm, cs, rg list
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br, err := New(context.Background(), tt.client, tt.clientSet)
			if err != nil {
				t.Fatalf("New() failed: %v", err)
			}

			readables := br.FetchResources(context.Background())

			if len(br.ErrorList) != tt.wantErrCount {
				t.Errorf("got %d errors, want %d. Errors: %v", len(br.ErrorList), tt.wantErrCount, br.ErrorList)
			}

			var readableNames []string
			for _, r := range readables {
				readableNames = append(readableNames, r.Name)
			}
			sort.Strings(readableNames)
			sort.Strings(tt.wantReadableNames)

			if diff := cmp.Diff(tt.wantReadableNames, readableNames); diff != "" {
				t.Errorf("FetchResources() returned unexpected readables (-want +got):\n%s", diff)
			}
		})
	}
}

type mockLogSource struct {
	returnError bool
	name        string
	readCloser  io.ReadCloser
}

var _ convertibleLogSourceIdentifiers = &mockLogSource{}

// fetchRcForLogSource implements convertibleLogSourceIdentifiers.
func (m *mockLogSource) fetchRcForLogSource(_ context.Context, _ coreClient) (io.ReadCloser, error) {
	if m.returnError {
		return nil, fmt.Errorf("failed to get RC")
	}

	return m.readCloser, nil
}

func (m *mockLogSource) pathName() string {
	return m.name
}

type mockReadCloser struct{}

var _ io.ReadCloser = &mockReadCloser{}

// Read implements io.ReadCloser.
func (m *mockReadCloser) Read(_ []byte) (n int, err error) {
	return 0, nil
}

// Close implements io.ReadCloser.
func (m *mockReadCloser) Close() error {
	return nil
}

// Sorting implementation allows for easy comparison during testing
func (ls logSources) Len() int {
	return len(ls)
}

func (ls logSources) Swap(i, j int) {
	ls[i], ls[j] = ls[j], ls[i]
}

func (ls logSources) Less(i, j int) bool {
	return ls[i].pathName() < ls[j].pathName()
}
