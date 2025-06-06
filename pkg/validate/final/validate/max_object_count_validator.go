// Copyright 2025 Google LLC
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
	"kpt.dev/configsync/pkg/importer/analyzer/ast"
	"kpt.dev/configsync/pkg/importer/analyzer/validation/system"
	"kpt.dev/configsync/pkg/status"
)

// MaxObjectCount verifies that the number of managed resources does not exceed
// the specified maximum.
func MaxObjectCount(maxN int) func([]ast.FileObject) status.MultiError {
	if maxN <= 0 {
		return noOpValidator
	}
	return func(objs []ast.FileObject) status.MultiError {
		foundN := len(objs)
		if foundN > maxN {
			return system.MaxObjectCountError(maxN, foundN)
		}
		return nil
	}
}

func noOpValidator([]ast.FileObject) status.MultiError {
	return nil
}
