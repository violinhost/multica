package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrchestrationProjectionIngressIsDefaultOff(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler.UpsertPluginOrchestrationProjection(recorder, pluginActionRequest(
		http.MethodPut,
		"/issues/not-an-issue/orchestration-projection",
		"",
		json.RawMessage(`{}`),
		map[string]string{"id": "not-an-issue"},
	))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("default-off ingress status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
