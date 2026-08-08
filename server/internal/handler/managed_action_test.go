package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/managedaction"
)

func TestManagedActionCapabilityDiscoveryAndEnablementTransport(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects", map[string]any{"title": "Managed action transport"})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(req.Context(), `DELETE FROM managed_action_enablement WHERE project_id=$1`, project.ID)
		req := withURLParam(newRequest("DELETE", "/api/projects/"+project.ID, nil), "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})

	w = httptest.NewRecorder()
	req = withURLParam(newRequest("GET", "/api/managed-actions/projects/"+project.ID, nil), "projectId", project.ID)
	testHandler.ListManagedActionCapabilities(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListManagedActionCapabilities: got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"Key"`) {
		t.Fatalf("capability transport emitted Go field names: %s", w.Body.String())
	}
	var discovery struct {
		Actions []managedaction.Capability `json:"actions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&discovery); err != nil {
		t.Fatalf("decode discovery: %v", err)
	}
	if len(discovery.Actions) != 1 || discovery.Actions[0] != (managedaction.Capability{Key: managedaction.ActionKey, Version: managedaction.ActionVersion, Workflow: managedaction.WorkflowW1}) {
		t.Fatalf("discovery = %#v", discovery.Actions)
	}

	w = httptest.NewRecorder()
	req = withURLParam(newRequest("PUT", "/api/managed-actions/projects/"+project.ID+"/enablement", map[string]any{"enabled": true}), "projectId", project.ID)
	testHandler.SetManagedActionEnablement(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SetManagedActionEnablement: got %d: %s", w.Code, w.Body.String())
	}
	var configured managedaction.Capability
	if err := json.NewDecoder(w.Body).Decode(&configured); err != nil {
		t.Fatalf("decode enablement: %v", err)
	}
	if configured != (managedaction.Capability{Key: managedaction.ActionKey, Version: managedaction.ActionVersion, Workflow: managedaction.WorkflowW1, Enabled: true}) {
		t.Fatalf("configured capability = %#v", configured)
	}

	w = httptest.NewRecorder()
	req = withURLParam(newRequest("GET", "/api/managed-actions/projects/"+project.ID, nil), "projectId", project.ID)
	testHandler.ListManagedActionCapabilities(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListManagedActionCapabilities after configure: got %d: %s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&discovery); err != nil {
		t.Fatalf("decode configured discovery: %v", err)
	}
	if len(discovery.Actions) != 1 || !discovery.Actions[0].Enabled {
		t.Fatalf("configured discovery = %#v", discovery.Actions)
	}
}
