package managedaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestReceiptJSONIncludesEveryScopeIdentity(t *testing.T) {
	receipt := Receipt{
		DispatchID:    "dispatch",
		WorkspaceID:   "workspace",
		ProjectID:     "project",
		ParentIssueID: "parent",
		ActionKey:     ActionKey,
		Generation:    1,
		State:         "analysis_queued",
		ChildIssueID:  "child",
		TaskID:        "task",
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	for key, want := range map[string]string{
		"dispatch_id": "dispatch", "workspace_id": "workspace", "project_id": "project", "parent_issue_id": "parent",
	} {
		if got[key] != want {
			t.Errorf("receipt %s = %#v, want %q; JSON tags must not collide", key, got[key], want)
		}
	}
}

func TestCapabilityJSONUsesStableSnakeCase(t *testing.T) {
	encoded, err := json.Marshal(Capability{Key: ActionKey, Version: ActionVersion, Workflow: WorkflowW1, Enabled: true})
	if err != nil {
		t.Fatalf("marshal capability: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode capability: %v", err)
	}
	for key, want := range map[string]any{
		"key": ActionKey, "version": ActionVersion, "workflow": WorkflowW1, "enabled": true,
	} {
		if got[key] != want {
			t.Errorf("capability %s = %#v, want %#v", key, got[key], want)
		}
	}
	for _, legacy := range []string{"Key", "Version", "Workflow", "Enabled"} {
		if _, ok := got[legacy]; ok {
			t.Errorf("capability emitted non-ABI field %q", legacy)
		}
	}
}

func TestRegistryIsFixedAndVersioned(t *testing.T) {
	spec, ok := FindSpec(ActionKey, ActionVersion)
	if !ok || spec.Workflow != WorkflowW1 {
		t.Fatalf("fixed W1 spec was not discoverable: %#v, %v", spec, ok)
	}
	if _, ok := FindSpec("plugin.marketplace.v1", "v1"); ok {
		t.Fatal("unexpected dynamic action accepted")
	}
}

func TestStartFailsClosedWithoutVerifier(t *testing.T) {
	request := Request{ActionKey: ActionKey, ActionVersion: ActionVersion, SchemaVersion: "v1", RequestID: "r", IdempotencyKey: "i", Workflow: WorkflowW1, InitialRole: "analysis", InitialStage: 1, ReleasePolicy: "draft", PrimaryAgentID: "00000000-0000-0000-0000-000000000001", ResourceIDs: []string{"00000000-0000-0000-0000-000000000002"}, RevisionFacts: json.RawMessage(`{"revision":"0123456789abcdef0123456789abcdef01234567"}`), AuthorityReceipt: json.RawMessage(`{}`), Source: "test", Actor: "00000000-0000-0000-0000-000000000003"}
	_, err := (&Service{}).Start(context.Background(), request)
	if !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("Start error = %v, want unavailable verifier", err)
	}
}

func TestStartRejectsUnknownActionBeforeVerifierLookup(t *testing.T) {
	_, err := (&Service{}).Start(context.Background(), Request{ActionKey: "unknown", ActionVersion: "v1"})
	if !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("Start error = %v, want unknown action", err)
	}
}

func TestValidateRequestRejectsMalformedNestedFacts(t *testing.T) {
	req := Request{SchemaVersion: "v1", RequestID: "r", IdempotencyKey: "i", ActionKey: ActionKey, ActionVersion: ActionVersion, Workflow: WorkflowW1, InitialRole: "analysis", InitialStage: 1, ReleasePolicy: "draft", PrimaryAgentID: "00000000-0000-0000-0000-000000000001", ResourceIDs: []string{"00000000-0000-0000-0000-000000000002"}, RevisionFacts: json.RawMessage(`{`), AuthorityReceipt: json.RawMessage(`{}`), Source: "test", Actor: "00000000-0000-0000-0000-000000000003"}
	if err := validateRequest(req, Spec{Key: ActionKey, Version: ActionVersion, Workflow: WorkflowW1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("validateRequest error = %v, want invalid request", err)
	}
}
