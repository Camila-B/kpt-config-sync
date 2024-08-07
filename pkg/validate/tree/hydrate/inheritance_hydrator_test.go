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

package hydrate

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	v1 "kpt.dev/configsync/pkg/api/configmanagement/v1"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/core/k8sobjects"
	"kpt.dev/configsync/pkg/importer/analyzer/ast"
	"kpt.dev/configsync/pkg/importer/analyzer/ast/node"
	"kpt.dev/configsync/pkg/importer/analyzer/validation"
	"kpt.dev/configsync/pkg/importer/filesystem/cmpath"
	"kpt.dev/configsync/pkg/kinds"
	"kpt.dev/configsync/pkg/status"
	"kpt.dev/configsync/pkg/validate/fileobjects"
)

func TestInheritance(t *testing.T) {
	testCases := []struct {
		name     string
		objs     *fileobjects.Tree
		want     *fileobjects.Tree
		wantErrs status.MultiError
	}{
		{
			name: "Preserve non-namespace objects",
			objs: &fileobjects.Tree{
				Repo: k8sobjects.Repo(),
				Cluster: []ast.FileObject{
					k8sobjects.ClusterRole(core.Name("hello-reader")),
					k8sobjects.ClusterRoleBinding(core.Name("hello-binding")),
				},
			},
			want: &fileobjects.Tree{
				Repo: k8sobjects.Repo(),
				Cluster: []ast.FileObject{
					k8sobjects.ClusterRole(core.Name("hello-reader")),
					k8sobjects.ClusterRoleBinding(core.Name("hello-binding")),
				},
			},
		},
		{
			name: "Propagate abstract namespace objects",
			objs: &fileobjects.Tree{
				Repo: k8sobjects.Repo(),
				HierarchyConfigs: []ast.FileObject{
					k8sobjects.HierarchyConfig(
						k8sobjects.HierarchyConfigResource(v1.HierarchyModeDefault, kinds.Role().GroupVersion(), kinds.Role().Kind),
					),
				},
				Tree: &ast.TreeNode{
					Relative: cmpath.RelativeSlash("namespaces"),
					Type:     node.AbstractNamespace,
					Objects: []ast.FileObject{
						k8sobjects.RoleAtPath("namespaces/role.yaml", core.Name("reader")),
					},
					Children: []*ast.TreeNode{
						{
							Relative: cmpath.RelativeSlash("namespaces/hello"),
							Type:     node.AbstractNamespace,
							Objects: []ast.FileObject{
								k8sobjects.RoleAtPath("namespaces/hello/role.yaml", core.Name("writer")),
							},
							Children: []*ast.TreeNode{
								{
									Relative: cmpath.RelativeSlash("namespaces/hello/world"),
									Type:     node.Namespace,
									Objects: []ast.FileObject{
										k8sobjects.Namespace("namespaces/hello/world"),
									},
								},
								{
									Relative: cmpath.RelativeSlash("namespaces/hello/moon"),
									Type:     node.Namespace,
									Objects: []ast.FileObject{
										k8sobjects.Namespace("namespaces/hello/moon"),
										k8sobjects.Deployment("namespaces/hello/moon"),
									},
								},
							},
						},
						{
							Relative: cmpath.RelativeSlash("namespaces/goodbye"),
							Type:     node.Namespace,
							Objects: []ast.FileObject{
								k8sobjects.Namespace("namespaces/goodbye"),
								k8sobjects.Deployment("namespaces/goodbye"),
							},
						},
					},
				},
			},
			want: &fileobjects.Tree{
				Repo: k8sobjects.Repo(),
				HierarchyConfigs: []ast.FileObject{
					k8sobjects.HierarchyConfig(
						k8sobjects.HierarchyConfigResource(v1.HierarchyModeDefault, kinds.Role().GroupVersion(), kinds.Role().Kind),
					),
				},
				Tree: &ast.TreeNode{
					Relative: cmpath.RelativeSlash("namespaces"),
					Type:     node.AbstractNamespace,
					Objects: []ast.FileObject{
						k8sobjects.RoleAtPath("namespaces/role.yaml", core.Name("reader")),
					},
					Children: []*ast.TreeNode{
						{
							Relative: cmpath.RelativeSlash("namespaces/hello"),
							Type:     node.AbstractNamespace,
							Objects: []ast.FileObject{
								k8sobjects.RoleAtPath("namespaces/hello/role.yaml", core.Name("writer")),
							},
							Children: []*ast.TreeNode{
								{
									Relative: cmpath.RelativeSlash("namespaces/hello/world"),
									Type:     node.Namespace,
									Objects: []ast.FileObject{
										k8sobjects.Namespace("namespaces/hello/world"),
										k8sobjects.RoleAtPath("namespaces/role.yaml", core.Name("reader")),
										k8sobjects.RoleAtPath("namespaces/hello/role.yaml", core.Name("writer")),
									},
								},
								{
									Relative: cmpath.RelativeSlash("namespaces/hello/moon"),
									Type:     node.Namespace,
									Objects: []ast.FileObject{
										k8sobjects.Namespace("namespaces/hello/moon"),
										k8sobjects.Deployment("namespaces/hello/moon"),
										k8sobjects.RoleAtPath("namespaces/role.yaml", core.Name("reader")),
										k8sobjects.RoleAtPath("namespaces/hello/role.yaml", core.Name("writer")),
									},
								},
							},
						},
						{
							Relative: cmpath.RelativeSlash("namespaces/goodbye"),
							Type:     node.Namespace,
							Objects: []ast.FileObject{
								k8sobjects.Namespace("namespaces/goodbye"),
								k8sobjects.Deployment("namespaces/goodbye"),
								k8sobjects.RoleAtPath("namespaces/role.yaml", core.Name("reader")),
							},
						},
					},
				},
			},
		},
		{
			name: "Validate Namespace can not have child Namespaces",
			objs: &fileobjects.Tree{
				Tree: &ast.TreeNode{
					Relative: cmpath.RelativeSlash("namespaces"),
					Type:     node.AbstractNamespace,
					Children: []*ast.TreeNode{
						{
							Relative: cmpath.RelativeSlash("namespaces/hello"),
							Type:     node.Namespace,
							Objects: []ast.FileObject{
								k8sobjects.Namespace("namespaces/hello"),
							},
							Children: []*ast.TreeNode{
								{
									Relative: cmpath.RelativeSlash("namespaces/hello/world"),
									Type:     node.Namespace,
									Objects: []ast.FileObject{
										k8sobjects.Namespace("namespaces/hello/world"),
									},
								},
								{
									Relative: cmpath.RelativeSlash("namespaces/hello/moon"),
									Type:     node.Namespace,
									Objects: []ast.FileObject{
										k8sobjects.Namespace("namespaces/hello/moon"),
									},
								},
							},
						},
					},
				},
			},
			wantErrs: status.Append(
				validation.IllegalNamespaceSubdirectoryError(
					&ast.TreeNode{Relative: cmpath.RelativeSlash("namespaces/hello/world")},
					&ast.TreeNode{Relative: cmpath.RelativeSlash("namespaces/hello")},
				),
				validation.IllegalNamespaceSubdirectoryError(
					&ast.TreeNode{Relative: cmpath.RelativeSlash("namespaces/hello/moon")},
					&ast.TreeNode{Relative: cmpath.RelativeSlash("namespaces/hello")},
				),
			),
		},
		{
			name: "Validate abstract namespace can not have invalid objects",
			objs: &fileobjects.Tree{
				Repo: k8sobjects.Repo(),
				HierarchyConfigs: []ast.FileObject{
					k8sobjects.HierarchyConfig(
						k8sobjects.HierarchyConfigResource(v1.HierarchyModeNone, kinds.Role().GroupVersion(), kinds.Role().Kind),
					),
				},
				Tree: &ast.TreeNode{
					Relative: cmpath.RelativeSlash("namespaces"),
					Type:     node.AbstractNamespace,
					Children: []*ast.TreeNode{
						{
							Relative: cmpath.RelativeSlash("namespaces/hello"),
							Type:     node.AbstractNamespace,
							Objects: []ast.FileObject{
								k8sobjects.RoleAtPath("namespaces/hello/role.yaml", core.Name("writer")),
							},
							Children: []*ast.TreeNode{
								{
									Relative: cmpath.RelativeSlash("namespaces/hello/world"),
									Type:     node.Namespace,
									Objects: []ast.FileObject{
										k8sobjects.Namespace("namespaces/hello/world"),
									},
								},
							},
						},
					},
				},
			},
			wantErrs: validation.IllegalAbstractNamespaceObjectKindError(k8sobjects.RoleAtPath("namespaces/hello/role.yaml", core.Name("writer"))),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errs := Inheritance(tc.objs)
			if !errors.Is(errs, tc.wantErrs) {
				t.Errorf("Got Inheritance() error %v, want %v", errs, tc.wantErrs)
			}
			if tc.want != nil {
				if diff := cmp.Diff(tc.want, tc.objs, ast.CompareFileObject); diff != "" {
					t.Error(diff)
				}
			}
		})
	}
}
