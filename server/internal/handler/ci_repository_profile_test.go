package handler

import (
	"context"
	"encoding/json"
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

func jsonContains(body []byte, key string) bool {
	var value map[string]any
	return json.Unmarshal(body, &value) == nil && value[key] != nil
}
