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
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/fake"
	"kpt.dev/configsync/cmd/nomos/util"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/client/restconfig"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/core/k8sobjects"
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
