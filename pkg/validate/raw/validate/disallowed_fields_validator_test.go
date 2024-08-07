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

package validate

import (
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/core/k8sobjects"
	"kpt.dev/configsync/pkg/importer/analyzer/ast"
	"kpt.dev/configsync/pkg/importer/analyzer/validation/syntax"
	"kpt.dev/configsync/pkg/importer/id"
	"kpt.dev/configsync/pkg/status"
	"kpt.dev/configsync/pkg/validate/fileobjects"
)

func TestDisallowedFields(t *testing.T) {
	testCases := []struct {
		name     string
		objs     *fileobjects.Raw
		wantErrs status.MultiError
	}{
		{
			name: "Deployment with allowed fields passes",
			objs: &fileobjects.Raw{
				Objects: []ast.FileObject{
					k8sobjects.Deployment("hello"),
				},
			},
		},
		{
			name: "Deployment with disallowed fields fails",
			objs: &fileobjects.Raw{
				Objects: []ast.FileObject{
					k8sobjects.Deployment("hello",
						core.OwnerReference([]metav1.OwnerReference{{}}),
						core.SelfLink("this-is-me"),
						core.UID("my-uid"),
						core.ResourceVersion("123456"),
						core.Generation(123456),
						core.CreationTimeStamp(metav1.NewTime(time.Now())),
						core.DeletionTimestamp(metav1.NewTime(time.Now())),
						core.DeletionGracePeriod(654321),
					),
				},
			},
			wantErrs: status.Wrap(
				syntax.IllegalFieldsInConfigError(k8sobjects.Deployment("hello"), id.OwnerReference),
				syntax.IllegalFieldsInConfigError(k8sobjects.Deployment("hello"), id.SelfLink),
				syntax.IllegalFieldsInConfigError(k8sobjects.Deployment("hello"), id.UID),
				syntax.IllegalFieldsInConfigError(k8sobjects.Deployment("hello"), id.ResourceVersion),
				syntax.IllegalFieldsInConfigError(k8sobjects.Deployment("hello"), id.Generation),
				syntax.IllegalFieldsInConfigError(k8sobjects.Deployment("hello"), id.CreationTimestamp),
				syntax.IllegalFieldsInConfigError(k8sobjects.Deployment("hello"), id.DeletionTimestamp),
				syntax.IllegalFieldsInConfigError(k8sobjects.Deployment("hello"), id.DeletionGracePeriodSeconds),
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errs := DisallowedFields(tc.objs)
			if !errors.Is(errs, tc.wantErrs) {
				t.Errorf("got DisallowedFields() error %v, want %v", errs, tc.wantErrs)
			}
		})
	}
}
