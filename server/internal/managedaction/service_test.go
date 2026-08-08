package managedaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

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
