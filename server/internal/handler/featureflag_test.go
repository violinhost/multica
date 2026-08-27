package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

func withComposioMCPAppsFlag(t *testing.T, h *Handler, enabled bool) {
	withFeatureFlag(t, h, featureflags.ComposioMCPApps, enabled)
}

func withPluginsV1Flag(t *testing.T, h *Handler, enabled bool) {
	withFeatureFlag(t, h, featureflags.PluginsV1, enabled)
}

func withCustomIssueStatusesFlag(t *testing.T, h *Handler, enabled bool) {
	withFeatureFlag(t, h, featureflags.CustomIssueStatuses, enabled)
}

func withAutomulticaOrchestrationProjectionFlag(t *testing.T, h *Handler, enabled bool) {
	withFeatureFlag(t, h, featureflags.AutomulticaOrchestrationProjection, enabled)
}

// withFeatureFlags installs one provider containing every requested decision.
// Do not compose withFeatureFlag calls: each one replaces the handler service.
func withFeatureFlags(t *testing.T, h *Handler, enabled map[string]bool) {
	t.Helper()
	provider := featureflag.NewStaticProvider()
	for key, value := range enabled {
		provider.Set(key, featureflag.Rule{Default: value})
	}
	flags := featureflag.NewService(provider)

	origHandlerFlags := h.FeatureFlags
	h.FeatureFlags = flags
	var origTaskFlags *featureflag.Service
	if h.TaskService != nil {
		origTaskFlags = h.TaskService.FeatureFlags
		h.TaskService.FeatureFlags = flags
	}
	t.Cleanup(func() {
		h.FeatureFlags = origHandlerFlags
		if h.TaskService != nil {
			h.TaskService.FeatureFlags = origTaskFlags
		}
	})
}

func withFeatureFlag(t *testing.T, h *Handler, key string, enabled bool) {
	t.Helper()
	provider := featureflag.NewStaticProvider()
	provider.Set(key, featureflag.Rule{Default: enabled})
	flags := featureflag.NewService(provider)

	origHandlerFlags := h.FeatureFlags
	h.FeatureFlags = flags
	var origTaskFlags *featureflag.Service
	if h.TaskService != nil {
		origTaskFlags = h.TaskService.FeatureFlags
		h.TaskService.FeatureFlags = flags
	}
	t.Cleanup(func() {
		h.FeatureFlags = origHandlerFlags
		if h.TaskService != nil {
			h.TaskService.FeatureFlags = origTaskFlags
		}
	})
}
