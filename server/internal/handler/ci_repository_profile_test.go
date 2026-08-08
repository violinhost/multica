package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/ciprofile"
)

type ciProfileTestVerifier struct{}

func (ciProfileTestVerifier) Verify(_ context.Context, evidence ciprofile.Evidence) (ciprofile.Attestation, error) {
	return ciprofile.Attestation{
		ProfileID: evidence.ProfileID, Generation: evidence.Generation, Revision: evidence.Revision,
		ProjectionDigest: evidence.ProjectionDigest, Reference: "test-attestation-reference",
	}, nil
}

func TestCIRepositoryProfileLifecycleAndReplay(t *testing.T) {
	project, resource := createCIProfileTestResource(t)
	t.Cleanup(func() {
		req := newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})

	request := ciProfileRegistration(project.ID, resource.ID)
	first := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "", request)
	if first.Code != http.StatusCreated {
		t.Fatalf("register: got %d: %s", first.Code, first.Body.String())
	}
	var registered ciprofile.Profile
	if err := json.NewDecoder(first.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered.Status != "pending_adapter" || registered.Eligible || registered.ReceiptID == "" {
		t.Fatalf("unexpected registration response: %+v", registered)
	}

	replay := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "", request)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay: got %d: %s", replay.Code, replay.Body.String())
	}
	var replayed ciprofile.Profile
	if err := json.NewDecoder(replay.Body).Decode(&replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.ReceiptID != registered.ReceiptID || replayed.ProfileID != registered.ProfileID {
		t.Fatalf("replay changed durable identity: first=%+v replay=%+v", registered, replayed)
	}

	request["revision"] = "1123456789abcdef0123456789abcdef01234567"
	conflict := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "", request)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed idempotency payload: got %d: %s", conflict.Code, conflict.Body.String())
	}

	originalVerifier := testHandler.CIProfileVerifier
	testHandler.CIProfileVerifier = ciProfileTestVerifier{}
	t.Cleanup(func() { testHandler.CIProfileVerifier = originalVerifier })
	enabled := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "/enable", map[string]any{"adapter_attestation": map[string]any{"opaque": "input"}})
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable: got %d: %s", enabled.Code, enabled.Body.String())
	}
	replayAfterEnable := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "", ciProfileRegistration(project.ID, resource.ID))
	if replayAfterEnable.Code != http.StatusOK {
		t.Fatalf("replay after enable: got %d: %s", replayAfterEnable.Code, replayAfterEnable.Body.String())
	}
	var afterEnable ciprofile.Profile
	if err := json.NewDecoder(replayAfterEnable.Body).Decode(&afterEnable); err != nil {
		t.Fatal(err)
	}
	if afterEnable.Status != "enabled" || !afterEnable.Eligible {
		t.Fatalf("replay did not return current lifecycle: %+v", afterEnable)
	}

	disabled := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "/disable", map[string]any{})
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable: got %d: %s", disabled.Code, disabled.Body.String())
	}
	discovery := callCIProfile(t, http.MethodGet, project.ID, resource.ID, "?revision=0123456789abcdef0123456789abcdef01234567", nil)
	if discovery.Code != http.StatusOK || jsonContains(discovery.Body.Bytes(), "attestation") {
		t.Fatalf("sanitized discovery: got %d: %s", discovery.Code, discovery.Body.String())
	}
}

func TestCIRepositoryProfileRejectsUnknownTransportFields(t *testing.T) {
	project, resource := createCIProfileTestResource(t)
	t.Cleanup(func() {
		req := newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})
	request := ciProfileRegistration(project.ID, resource.ID)
	request["runner"] = "privileged"
	resp := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "", request)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unsafe field: got %d: %s", resp.Code, resp.Body.String())
	}
	var profiles int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM ci_repository_profile WHERE project_id=$1`, project.ID).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 {
		t.Fatalf("unsafe request materialized %d profiles", profiles)
	}
}

func TestCIRepositoryProfileRejectsDisablePayloadWithoutMutation(t *testing.T) {
	project, resource := createCIProfileTestResource(t)
	t.Cleanup(func() {
		req := newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})
	registered := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "", ciProfileRegistration(project.ID, resource.ID))
	if registered.Code != http.StatusCreated {
		t.Fatalf("register: %d: %s", registered.Code, registered.Body.String())
	}

	req := rawCIProfileRequest(http.MethodPost, project.ID, resource.ID, "/disable", []byte(`{"provider":"github-hosted"}`))
	w := httptest.NewRecorder()
	testHandler.DisableCIRepositoryProfile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("forbidden disable payload: got %d: %s", w.Code, w.Body.String())
	}
	var status string
	var auditCount int
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM ci_repository_profile WHERE project_id=$1`, project.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM ci_repository_profile_audit WHERE project_id=$1`, project.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if status != "pending_adapter" || auditCount != 1 {
		t.Fatalf("forbidden disable mutated profile: status=%q audits=%d", status, auditCount)
	}
}

func TestCIRepositoryProfileRejectsRepositoryRetarget(t *testing.T) {
	project, resource := createCIProfileTestResource(t)
	t.Cleanup(func() {
		req := newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})
	if response := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "", ciProfileRegistration(project.ID, resource.ID)); response.Code != http.StatusCreated {
		t.Fatalf("register: %d: %s", response.Code, response.Body.String())
	}
	originalVerifier := testHandler.CIProfileVerifier
	testHandler.CIProfileVerifier = ciProfileTestVerifier{}
	t.Cleanup(func() { testHandler.CIProfileVerifier = originalVerifier })
	if response := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "/enable", map[string]any{"adapter_attestation": map[string]any{"opaque": "input"}}); response.Code != http.StatusOK {
		t.Fatalf("enable: %d: %s", response.Code, response.Body.String())
	}

	updateReq := newRequest(http.MethodPut, "/api/projects/"+project.ID+"/resources/"+resource.ID, map[string]any{
		"resource_ref": map[string]any{"url": "https://github.com/example/retargeted.git"},
	})
	updateReq = withURLParams(updateReq, "id", project.ID, "resourceId", resource.ID)
	update := httptest.NewRecorder()
	testHandler.UpdateProjectResource(update, updateReq)
	if update.Code != http.StatusConflict {
		t.Fatalf("retarget: got %d: %s", update.Code, update.Body.String())
	}

	discovery := callCIProfile(t, http.MethodGet, project.ID, resource.ID, "?revision=0123456789abcdef0123456789abcdef01234567", nil)
	if discovery.Code != http.StatusOK {
		t.Fatalf("discovery after retarget rejection: %d: %s", discovery.Code, discovery.Body.String())
	}
	var profile ciprofile.Profile
	if err := json.NewDecoder(discovery.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	if profile.Repository != "violinhost/multica" || profile.Status != "enabled" || !profile.Eligible {
		t.Fatalf("retarget changed profile semantics: %+v", profile)
	}
}

func TestCIRepositoryProfileResourceDeleteRollsBackTombstone(t *testing.T) {
	project, resource := createCIProfileTestResource(t)
	t.Cleanup(func() {
		req := newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})
	if response := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "", ciProfileRegistration(project.ID, resource.ID)); response.Code != http.StatusCreated {
		t.Fatalf("register: %d: %s", response.Code, response.Body.String())
	}
	originalVerifier := testHandler.CIProfileVerifier
	testHandler.CIProfileVerifier = ciProfileTestVerifier{}
	t.Cleanup(func() { testHandler.CIProfileVerifier = originalVerifier })
	if response := callCIProfile(t, http.MethodPost, project.ID, resource.ID, "/enable", map[string]any{"adapter_attestation": map[string]any{"opaque": "input"}}); response.Code != http.StatusOK {
		t.Fatalf("enable: %d: %s", response.Code, response.Body.String())
	}

	const functionName = "ci_profile_test_reject_resource_delete"
	const triggerName = "ci_profile_test_reject_resource_delete_trigger"
	if _, err := testPool.Exec(context.Background(), fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			IF OLD.id::text = TG_ARGV[0] THEN RAISE EXCEPTION 'forced resource delete failure'; END IF;
			RETURN OLD;
		END;
		$$ LANGUAGE plpgsql`, functionName)); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON project_resource
		FOR EACH ROW EXECUTE FUNCTION %s('%s')`, triggerName, functionName, resource.ID)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), "DROP TRIGGER IF EXISTS "+triggerName+" ON project_resource")
		_, _ = testPool.Exec(context.Background(), "DROP FUNCTION IF EXISTS "+functionName+"()")
	})

	deleteReq := newRequest(http.MethodDelete, "/api/projects/"+project.ID+"/resources/"+resource.ID, nil)
	deleteReq = withURLParams(deleteReq, "id", project.ID, "resourceId", resource.ID)
	deleteResponse := httptest.NewRecorder()
	testHandler.DeleteProjectResource(deleteResponse, deleteReq)
	if deleteResponse.Code != http.StatusInternalServerError {
		t.Fatalf("forced delete failure: got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	var status string
	var auditCount int
	var resources int
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM ci_repository_profile WHERE project_id=$1`, project.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM ci_repository_profile_audit WHERE project_id=$1`, project.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM project_resource WHERE id=$1`, resource.ID).Scan(&resources); err != nil {
		t.Fatal(err)
	}
	if status != "enabled" || auditCount != 2 || resources != 1 {
		t.Fatalf("delete failure left partial state: status=%q audits=%d resources=%d", status, auditCount, resources)
	}
}

func createCIProfileTestResource(t *testing.T) (ProjectResponse, ProjectResourceResponse) {
	t.Helper()
	projectRecorder := httptest.NewRecorder()
	testHandler.CreateProject(projectRecorder, newRequest(http.MethodPost, "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "CI profile test project"}))
	if projectRecorder.Code != http.StatusCreated {
		t.Fatalf("create project: %d: %s", projectRecorder.Code, projectRecorder.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(projectRecorder.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	resourceRecorder := httptest.NewRecorder()
	resourceRequest := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/resources", map[string]any{"resource_type": "github_repo", "resource_ref": map[string]any{"url": "git@github.com:ViolinHost/Multica.git"}})
	resourceRequest = withURLParam(resourceRequest, "id", project.ID)
	testHandler.CreateProjectResource(resourceRecorder, resourceRequest)
	if resourceRecorder.Code != http.StatusCreated {
		t.Fatalf("create resource: %d: %s", resourceRecorder.Code, resourceRecorder.Body.String())
	}
	var resource ProjectResourceResponse
	if err := json.NewDecoder(resourceRecorder.Body).Decode(&resource); err != nil {
		t.Fatal(err)
	}
	return project, resource
}

func ciProfileRegistration(projectID, resourceID string) map[string]any {
	return map[string]any{
		"schema_version": "ci.repository-profile.v1", "request_id": "request-1", "workspace_id": testWorkspaceID,
		"project_id": projectID, "resource_id": resourceID, "revision": "0123456789abcdef0123456789abcdef01234567",
		"workflow_class": "native_ci", "workflow_version": "v1", "job_class": "backend", "check_name": "backend",
		"service_classes": []string{"postgresql_pgvector", "redis"}, "hosted_fallback": false,
		"adapter_attestation": map[string]any{"opaque": "input"}, "source": "owner_admin",
	}
}

func callCIProfile(t *testing.T, method, projectID, resourceID, suffix string, body any) *httptest.ResponseRecorder {
	t.Helper()
	pathSuffix := suffix
	if len(suffix) > 0 && suffix[0] == '?' {
		pathSuffix = suffix
	} else if suffix != "" {
		pathSuffix = suffix
	}
	req := newRequest(method, "/api/projects/"+projectID+"/resources/"+resourceID+"/ci-profile"+pathSuffix, body)
	req = withURLParams(req, "id", projectID, "resourceId", resourceID)
	w := httptest.NewRecorder()
	switch {
	case method == http.MethodGet:
		testHandler.GetCIRepositoryProfileDiscovery(w, req)
	case suffix == "/enable":
		testHandler.EnableCIRepositoryProfile(w, req)
	case suffix == "/disable":
		testHandler.DisableCIRepositoryProfile(w, req)
	default:
		testHandler.RegisterCIRepositoryProfile(w, req)
	}
	return w
}

func rawCIProfileRequest(method, projectID, resourceID, suffix string, body []byte) *http.Request {
	req := httptest.NewRequest(method, "/api/projects/"+projectID+"/resources/"+resourceID+"/ci-profile"+suffix, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	return withURLParams(req, "id", projectID, "resourceId", resourceID)
}

func jsonContains(body []byte, key string) bool {
	var value map[string]any
	return json.Unmarshal(body, &value) == nil && value[key] != nil
}
