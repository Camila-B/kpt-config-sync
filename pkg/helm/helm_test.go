// Copyright 2024 Google LLC
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

package helm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kpt.dev/configsync/pkg/api/configsync"
)

// fileExists checks if a file exists and is not a directory.
func fileExists(filePath string) bool {
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

func TestHelmTemplate_ValuesFileCleanup(t *testing.T) {
	tempDir := t.TempDir()

	h := &Hydrator{
		Chart:       "mychart",
		Repo:        "https://example.com/charts", // Non-OCI to avoid login, non-empty
		Version:     "1.0.0",                      // Specific version to avoid 'helm show chart'
		ValuesYAML:  "replicaCount: 2\nimage:\n  tag: latest",
		HydrateRoot: tempDir,
		Dest:        "mychart-rendered",
		Auth:        configsync.AuthNone,
		Namespace:   "default", // Provide a namespace
		// CACertFilePath can be empty if not needed for the test's mock.
	}

	expectedTempValuesFile := filepath.Join(os.TempDir(), valuesFile) // valuesFile is "chart-values.yaml"

	// Ensure the temp file doesn't exist before the test (it shouldn't, but good for sanity)
	if fileExists(expectedTempValuesFile) {
		t.Fatalf("Temporary values file %q unexpectedly exists before test execution", expectedTempValuesFile)
	}

	// Mock helmExecCommand
	origHelmExecCommand := helmExecCommand
	helmExecCommand = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		t.Logf("Mock helmExecCommand called with: %s %v", command, args)
		if command != "helm" {
			t.Fatalf("Expected 'helm' command, got '%s'", command)
		}
		// Simulate success for 'helm template'
		// The actual 'helm template' command writes to --output-dir,
		// so the mock needs to replicate creating the destination directory structure
		// for the parts of HelmTemplate that run after the helm command (like symlink creation).
		if len(args) > 0 && args[0] == "template" {
			var outputDir string
			for i, arg := range args {
				if arg == "--output-dir" && i+1 < len(args) {
					outputDir = args[i+1]
					break
				}
			}
			if outputDir == "" {
				t.Fatal("--output-dir not found in helm template args")
			}
			// Create the chart directory inside the output directory, as Helm would.
			// h.Chart is 'mychart' in this test.
			chartSpecificDir := filepath.Join(outputDir, h.Chart)
			if err := os.MkdirAll(chartSpecificDir, 0755); err != nil {
				t.Fatalf("Failed to create mock chart output directory %s: %v", chartSpecificDir, err)
			}
			return exec.Command("true") // Simulate successful helm template command
		}
		// For any other helm command (like potential 'helm registry login' if repo was OCI, or 'helm show chart' if version was a range),
		// also return true for simplicity in this test, as we are focusing on values file cleanup.
		return exec.Command("true")
	}
	defer func() { helmExecCommand = origHelmExecCommand }() // Restore

	// Execute HelmTemplate
	err := h.HelmTemplate(context.Background())
	// We don't expect an error from HelmTemplate itself if the mock is set up correctly.
	// The main goal is to check file cleanup.
	if err != nil {
		// If UpdateSymlink fails, it could be because the destDir (outputDir/h.Version) wasn't created.
		// The mock for "helm template" now creates outputDir/h.Chart. We also need outputDir (destDir in HelmTemplate func) itself.
		// destDir in HelmTemplate is filepath.Join(h.HydrateRoot, h.Version)
		expectedDestDir := filepath.Join(h.HydrateRoot, h.Version)
		if _, statErr := os.Stat(expectedDestDir); os.IsNotExist(statErr) {
			t.Logf("Note: destDir %s was not created by mock, this might cause UpdateSymlink to fail if not handled by mock.", expectedDestDir)
		}
		// For this test, we are primarily interested in the cleanup, which happens in a defer.
		// So, we log the error but proceed to check the cleanup.
		t.Logf("HelmTemplate returned an error: %v. Proceeding to check cleanup.", err)
	}

	// Assert: Temporary values file should be removed
	if fileExists(expectedTempValuesFile) {
		// For debugging, let's see the content if it exists
		content, readErr := os.ReadFile(expectedTempValuesFile)
		if readErr != nil {
			t.Logf("Could not read content of unexpected file %s: %v", expectedTempValuesFile, readErr)
		} else {
			t.Logf("Content of unexpected file %s:\n%s", expectedTempValuesFile, string(content))
		}
		t.Errorf("Expected temporary values file %q to be removed, but it still exists.", expectedTempValuesFile)
	}
}

func TestIsRange(t *testing.T) {
	testCases := []struct {
		name    string
		version string
		isRange bool
	}{
		{name: "empty version", version: "", isRange: true},
		{name: "valid semver", version: "1.2.3", isRange: false},
		{name: "valid semver with v prefix", version: "v1.2.3", isRange: false},
		{name: "simple range", version: "^1.0.0", isRange: true},
		{name: "range with spaces", version: ">=1.2.3 <2.0.0", isRange: true},
		{name: "invalid semver without range characters", version: "1.2", isRange: false}, //This will fail NewConstraint too
		{name: "patch version range", version: "~1.2.3", isRange: true},
		{name: "minor version range", version: "1.2.x", isRange: true},
		{name: "major version range", version: "1.x", isRange: true},
		{name: "hyphen range", version: "1.0.0 - 2.0.0", isRange: true},
		{name: "invalid range", version: "->1.2.3", isRange: false},
		{name: "latest", version: "latest", isRange: false}, // 'latest' is a specific tag, not a semver range
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRange(tc.version)
			if got != tc.isRange {
				t.Errorf("isRange(%q) = %v, want %v", tc.version, got, tc.isRange)
			}
		})
	}
}
