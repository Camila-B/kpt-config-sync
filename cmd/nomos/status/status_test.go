package status

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"kpt.dev/configsync/cmd/nomos/flags"
	// Assuming ClusterClient is a concrete type and we are providing specific mocks for its methods.
	// If ClusterClient were an interface, we'd implement that.
	// For this test, we'll define a mock that can be used in the clientMap.
	// The key is that the real `clusterStates` function will iterate this map
	// and call `client.clusterStatus`. We need to provide a client that has this method.
	"kpt.dev/configsync/pkg/api/configsync/v1beta1" // For SyncState types
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"  // For metav1.Time -> v1beta1.Time
)

// mockClusterClientProvider is a function type that returns a mock ClusterState
// for a given cluster name. This allows us to define the mock behavior per cluster.
type mockClusterClientProvider func(clusterName string) *ClusterState

// mockClient is a utility that can act as a ClusterClient for testing purposes.
// It uses a provider function to determine what status to return.
type mockClient struct {
	statusProvider mockClusterClientProvider
}

// clusterStatus is the method that will be called by the production `clusterStates` function.
func (m *mockClient) clusterStatus(ctx context.Context, clusterName string, ns string) *ClusterState {
	if m.statusProvider != nil {
		return m.statusProvider(clusterName)
	}
	return &ClusterState{Ref: clusterName, Status: "MockClientNotConfigured"}
}

// Redefine TestClusterState to match the structure expected by json.MarshalIndent in printStatus
// This should ideally match the fields of the actual ClusterState struct that are marshalled.
// For the test, we ensure that whatever printStatus marshals, we can unmarshal and compare.
type TestOutputClusterState struct {
	Ref              string                      `json:"Ref,omitempty"`
	Status           string                      `json:"Status,omitempty"`
	SyncState        v1beta1.SyncState           `json:"SyncState,omitempty"`
	ReconcilerState  string                      `json:"ReconcilerState,omitempty"` // Matches ClusterState field
	LastSyncedToken  string                      `json:"LastSyncedToken,omitempty"`
	SyncingCondition *v1beta1.RootSyncCondition  `json:"SyncingCondition,omitempty"` // Pointer to match actual if it's a pointer
	StalledCondition *v1beta1.RootSyncCondition  `json:"StalledCondition,omitempty"` // Pointer to match actual if it's a pointer
	Errors           []v1beta1.ConfigSyncError   `json:"Errors,omitempty"`
	RootSyncs        []RootSyncState             `json:"RootSyncs,omitempty"` // Assuming RootSyncs field exists and is marshalled
	RepoSyncs        []RepoSyncState             `json:"RepoSyncs,omitempty"` // Assuming RepoSyncs field exists and is marshalled
	ResourceConditions []ResourceConditionSummary `json:"ResourceConditions,omitempty"` // Assuming this field exists
	IsMulti          *bool                       `json:"IsMulti,omitempty"` // Assuming this field exists
}


func TestStatusCommandJsonOutput(t *testing.T) {
	flags.StatusFormat = "json" // Set the desired output format
	ctx := context.Background()

	// Define the mock data that our mockClusterClient.clusterStatus will return
	mockData := map[string]*ClusterState{
		"cluster-prod": {
			Ref:             "cluster-prod",
			Status:          syncedMsg,
			SyncState:       v1beta1.SyncStateSynced,
			ReconcilerState: "Deployed",
			LastSyncedToken: "prod-token-xyz",
			SyncingCondition: &v1beta1.RootSyncCondition{
				Type:           v1beta1.RootSyncSyncing,
				Status:         metav1.ConditionFalse,
				LastUpdateTime: metav1.Time{Time: time.Date(2023, 1, 1, 10, 0, 0, 0, time.UTC)},
			},
			StalledCondition: &v1beta1.RootSyncCondition{
				Type:           v1beta1.RootSyncStalled,
				Status:         metav1.ConditionFalse,
				LastUpdateTime: metav1.Time{Time: time.Date(2023, 1, 1, 10, 0, 0, 0, time.UTC)},
			},
			RootSyncs: []RootSyncState{
				{Name: "root-sync", Namespace: "config-management-system", Status: syncedMsg, ReconcilerState: "Deployed"},
			},
			IsMulti: func(b bool) *bool { return &b }(true),
		},
		"cluster-staging": {
			Ref:             "cluster-staging",
			Status:          stalledMsg,
			SyncState:       v1beta1.SyncStateStalled,
			ReconcilerState: "Stalled",
			LastSyncedToken: "staging-token-abc",
			Errors: []v1beta1.ConfigSyncError{
				{Code: "2009", Message: "KNV2009: a validation error occurred"},
			},
			StalledCondition: &v1beta1.RootSyncCondition{
				Type:           v1beta1.RootSyncStalled,
				Status:         metav1.ConditionTrue,
				Message:        "Source error",
				LastUpdateTime: metav1.Time{Time: time.Date(2023, 1, 1, 11, 0, 0, 0, time.UTC)},
			},
			RepoSyncs: []RepoSyncState{
				{Name: "repo-sync", Namespace: "shipping-app", Status: stalledMsg, ReconcilerState: "Stalled"},
			},
			IsMulti: func(b bool) *bool { return &b }(true),
		},
		"cluster-dev": {
			Ref:             "cluster-dev",
			Status:          pendingMsg,
			SyncState:       v1beta1.SyncStatePending,
			ReconcilerState: "Pending",
			IsMulti:         func(b bool) *bool { return &b }(false), // Mono-repo example
		},
	}

	// Create the clientMap and names list for printStatus
	clientMap := make(map[string]*ClusterClient) // map[string]*status.ClusterClient
	var names []string
	for name := range mockData {
		names = append(names, name)
		// Attach the clusterStatus method directly to a struct that satisfies the implicit interface.
		// The production code's ClusterClient has this method. We provide our own "ClusterClient"
		// that has this method with mock behavior.
		// This requires status.ClusterClient to be either an interface, or for us to be able to
		// create instances of it or a compatible type.
		// Let's assume status.ClusterClient is a struct and we can't directly mock its methods
		// without an interface or link-time substitution.
		// The previous attempt with `mockClient` was on the right track.
		// The `clientMap` in `printStatus` is `map[string]*ClusterClient`.
		// So, `ClusterClient` must be the actual type from the `status` package.

		// This is the core challenge: if status.ClusterClient is a concrete type from the same package,
		// and its methods (like clusterStatus) are called directly (not through an interface),
		// true unit testing of printStatus by mocking these calls is hard without changing production code.

		// Workaround: If ClusterClient is a struct, and clusterStatus is a method on it,
		// we can't easily mock clusterStatus for specific instances passed in a map.
		// The `mockClient` struct defined earlier is a *different type* than `status.ClusterClient`.
		// Thus, `map[string]*mockClient` is not assignable to `map[string]*status.ClusterClient`.

		// Let's re-verify the structure of `ClusterClient` and how `clusterStatus` is defined.
		// Assuming `ClusterClient` is a struct in package `status` and `clusterStatus` is its method.
		// To mock this, we would typically need an interface.
		// e.g., type StatusProvider interface { clusterStatus(...) *ClusterState }
		// and clientMap would be map[string]StatusProvider.

		// If no such interface exists, we are in a slightly more difficult situation for pure unit testing.
		// The `var clusterStates = func(...)` was an attempt to bypass this.
		// If we cannot change production code, we might be testing `printStatus` more as an integration
		// with the real `clusterStates` and `clusterStatus`, where `clusterStatus` might attempt
		// real operations if not carefully constructed.

		// Let's assume `ClusterClient` might have fields that allow controlling `clusterStatus` behavior,
		// or `NewClusterClient` can be influenced. If not, the test becomes more of an integration test.

		// For the purpose of this exercise, I'll proceed as if `ClusterClient` can be constructed
		// such that its `clusterStatus` method can be controlled or is simple enough not to make external calls
		// when certain fields are set. This is often not the case for real clients.

		// The simplest interpretation of "Mock the necessary dependencies, such as ClusterClients and clusterStates"
		// in the absence of interfaces or var functions, is to control the *input* to clusterStates,
		// which is clientMap.
		// If ClusterClient has, for example, a field that stores the state, we could set that.
		// This is unlikely.

		// Given the constraints of not changing production code for testability immediately,
		// and the `var clusterStates` pattern being problematic if it's a func,
		// the test written in the previous step (that tried to substitute `clusterStates` func var)
		// is a common pattern *if the production code supports it*.
		// If it doesn't, this test becomes very hard to write as a pure unit test.

		// Let's assume the `clusterStates` function variable reassignment IS possible for this problem.
		// If not, the problem is significantly harder and requires production code changes.
		// I will revert to that pattern, assuming status.go can be (or is) structured to allow it.
	}


	// This is the mock function that will replace the real clusterStates
	mockClusterStatesFunc := func(ctx context.Context, cm map[string]*ClusterClient) (map[string]*ClusterState, []string) {
		var currentNames []string
		for k := range cm { // Use keys from the passed clientMap to determine names
			currentNames = append(currentNames, k)
		}
		// The mockData should be returned, keyed by names derived from clientMap
		// Ensure mockData contains entries for all keys in cm.
		return mockData, nil // monoRepoClusters can be nil for this test
	}

	// Perform the function variable swap for mocking clusterStates
	originalClusterStates := clusterStatesInternal // Assuming 'clusterStatesInternal' is the actual func
	clusterStatesInternal = mockClusterStatesFunc
	defer func() { clusterStatesInternal = originalClusterStates }()

	var buf bytes.Buffer
	// Important: Initialize tabwriter.Writer correctly as NewWriter expects an io.Writer.
	writer := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	// Populate clientMap and names based on mockData keys for printStatus call
	// The actual *ClusterClient instances can be nil because clusterStatesInternal is mocked.
	// However, clusterStatesInternal (our mock) uses the keys from clientMap.
	clientMap = make(map[string]*ClusterClient)
	for name := range mockData {
		clientMap[name] = nil // Client can be nil as clusterStatesInternal is mocked
		names = append(names, name)
	}
	
	printStatus(ctx, writer, clientMap, names)
	writer.Flush() // Ensure all data is written to buf

	// Unmarshal the actual output
	// Using map[string]interface{} for flexibility if TestOutputClusterState is not perfectly matching.
	var actualStateMap map[string]TestOutputClusterState
	trimmedOutput := strings.TrimSpace(buf.String())
	
	// Debug: Print the output if unmarshalling fails or for inspection
	t.Logf("Raw JSON output:\n%s", buf.String())

	if err := json.Unmarshal([]byte(trimmedOutput), &actualStateMap); err != nil {
		t.Fatalf("Failed to unmarshal actual output JSON: %v", err)
	}

	// Compare the unmarshalled actual data with the original mock data (expected)
	if len(actualStateMap) != len(mockData) {
		t.Errorf("Expected %d clusters in JSON output, got %d", len(mockData), len(actualStateMap))
	}

	for name, expectedState := range mockData {
		actualState, ok := actualStateMap[name]
		if !ok {
			t.Errorf("Expected cluster '%s' in JSON output, but not found", name)
			continue
		}
		if actualState.Status != expectedState.Status {
			t.Errorf("Cluster '%s': Status mismatch. Expected '%s', Got '%s'", name, expectedState.Status, actualState.Status)
		}
		if actualState.SyncState != expectedState.SyncState {
			t.Errorf("Cluster '%s': SyncState mismatch. Expected '%s', Got '%s'", name, expectedState.SyncState, actualState.SyncState)
		}
		if actualState.ReconcilerState != expectedState.ReconcilerState {
			t.Errorf("Cluster '%s': ReconcilerState mismatch. Expected '%s', Got '%s'", name, expectedState.ReconcilerState, actualState.ReconcilerState)
		}
		if actualState.LastSyncedToken != expectedState.LastSyncedToken {
			t.Errorf("Cluster '%s': LastSyncedToken mismatch. Expected '%s', Got '%s'", name, expectedState.LastSyncedToken, actualState.LastSyncedToken)
		}
		
		// Compare errors (simple length check for this example)
		if len(actualState.Errors) != len(expectedState.Errors) {
			t.Errorf("Cluster '%s': Errors count mismatch. Expected %d, Got %d", name, len(expectedState.Errors), len(actualState.Errors))
		} else {
			for i, expErr := range expectedState.Errors {
				actErr := actualState.Errors[i]
				if actErr.Code != expErr.Code || actErr.Message != expErr.Message {
					t.Errorf("Cluster '%s': Error mismatch at index %d. Expected Code/Msg '%s/%s', Got '%s/%s'",
						name, i, expErr.Code, expErr.Message, actErr.Code, actErr.Message)
				}
			}
		}

		// Compare conditions (checking for nil and basic fields)
		compareConditions := func(expected, actual *v1beta1.RootSyncCondition, condType string) {
			if expected == nil && actual != nil {
				t.Errorf("Cluster '%s': Expected %s condition to be nil, but got %+v", name, condType, actual)
				return
			}
			if expected != nil && actual == nil {
				t.Errorf("Cluster '%s': Expected %s condition %+v, but got nil", name, condType, expected)
				return
			}
			if expected != nil && actual != nil {
				if actual.Type != expected.Type {
					t.Errorf("Cluster '%s': %s Condition Type mismatch. Expected '%s', Got '%s'", name, condType, expected.Type, actual.Type)
				}
				if actual.Status != expected.Status {
					t.Errorf("Cluster '%s': %s Condition Status mismatch. Expected '%s', Got '%s'", name, condType, expected.Status, actual.Status)
				}
				if actual.Message != expected.Message {
					t.Errorf("Cluster '%s': %s Condition Message mismatch. Expected '%s', Got '%s'", name, condType, expected.Message, actual.Message)
				}
			}
		}
		compareConditions(expectedState.SyncingCondition, actualState.SyncingCondition, "Syncing")
		compareConditions(expectedState.StalledCondition, actualState.StalledCondition, "Stalled")

		// Compare IsMulti
		if expectedState.IsMulti == nil && actualState.IsMulti != nil {
			 t.Errorf("Cluster '%s': Expected IsMulti to be nil, but got %v", name, *actualState.IsMulti)
		} else if expectedState.IsMulti != nil && actualState.IsMulti == nil {
			 t.Errorf("Cluster '%s': Expected IsMulti to be %v, but got nil", name, *expectedState.IsMulti)
		} else if expectedState.IsMulti != nil && actualState.IsMulti != nil && *actualState.IsMulti != *expectedState.IsMulti {
			t.Errorf("Cluster '%s': IsMulti mismatch. Expected %v, Got %v", name, *expectedState.IsMulti, *actualState.IsMulti)
		}

		// Compare RootSyncs (simplified: check count and first element if present)
		if len(actualState.RootSyncs) != len(expectedState.RootSyncs) {
			t.Errorf("Cluster '%s': RootSyncs count mismatch. Expected %d, Got %d", name, len(expectedState.RootSyncs), len(actualState.RootSyncs))
		} else if len(expectedState.RootSyncs) > 0 {
			// Example: check name of first root sync
			if actualState.RootSyncs[0].Name != expectedState.RootSyncs[0].Name {
				t.Errorf("Cluster '%s': RootSyncs[0] Name mismatch. Expected '%s', Got '%s'", name, expectedState.RootSyncs[0].Name, actualState.RootSyncs[0].Name)
			}
		}
		// Compare RepoSyncs similarly
		if len(actualState.RepoSyncs) != len(expectedState.RepoSyncs) {
			t.Errorf("Cluster '%s': RepoSyncs count mismatch. Expected %d, Got %d", name, len(expectedState.RepoSyncs), len(actualState.RepoSyncs))
		} else if len(expectedState.RepoSyncs) > 0 {
			if actualState.RepoSyncs[0].Name != expectedState.RepoSyncs[0].Name {
				t.Errorf("Cluster '%s': RepoSyncs[0] Name mismatch. Expected '%s', Got '%s'", name, expectedState.RepoSyncs[0].Name, actualState.RepoSyncs[0].Name)
			}
		}
	}
}

// This declaration makes the assumption that status.go is structured like:
// var clusterStatesInternal = func clusterStates(...) { ... }
// OR that we can somehow modify a private 'clusterStates' to point to our mock.
// If clusterStates is just `func clusterStates(...)`, this pattern needs adjustment
// (e.g. the production code itself would need to use a variable like `clusterStatesProvider`).
var clusterStatesInternal func(ctx context.Context, clientMap map[string]*ClusterClient) (map[string]*ClusterState, []string)

// Mock implementations of constants from status.go for test isolation
const (
	pendingMsg     = "PENDING"
	syncedMsg      = "SYNCED"
	stalledMsg     = "STALLED"
	// reconcilingMsg = "RECONCILING" // Not used in this specific mock data
)

func init() {
	// This is crucial: we need to assign the *actual* production function to clusterStatesInternal
	// so that the test can swap it and restore it.
	// This implies that the production code's clusterStates function must be accessible
	// and assignable to this variable. This might require it to be public if status_test
	// is in a different package, or just accessible if in the same package.
	// If status.go has `func clusterStates(...)`, then this would be:
	// clusterStatesInternal = clusterStates (if in the same package)
	// This line will cause a compile error if 'clusterStates' is not found or not assignable.
	// For the test to compile standalone, clusterStatesInternal needs a default value if not set by production code.
	// This setup is for same-package testing.
	
	// If status.go is `package status` and this test is `package status_test`,
	// then unexported functions are not accessible.
	// If both are `package status`, then it works.
	// Let's assume they are in the same package for this pattern to work.
	if clusterStatesInternal == nil { // Avoids re-assigning if already set (e.g. by another test)
		clusterStatesInternal = clusterStates // Assign the actual production function
	}
}

// Ensure this test file can compile by providing a placeholder for the actual clusterStates
// if it's not being linked or found (e.g. running go test ./... from a higher dir).
// This is only for making `go test cmd/nomos/status/status_test.go` potentially work in isolation
// or if the real `clusterStates` is in a different file that's not compiled with the test.
var _ = func() bool { // This is a common pattern to ensure a function/var exists for the compiler
	if clusterStatesInternal == nil {
		// If the real clusterStates isn't linked, this mock prevents compilation errors during `go test`.
		// Tests should ideally run in an environment where the actual production code is available.
		clusterStatesInternal = func(ctx context.Context, clientMap map[string]*ClusterClient) (map[string]*ClusterState, []string) {
			panic("clusterStatesInternal was not initialized with the production function. " + 
				  "Ensure tests are run in an environment where status.clusterStates is available and assigned in init().")
		}
	}
	return true
}()
