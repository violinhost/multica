// Helper tests for velafi-quick-add. Full handler e2e tests require
// DB + middleware harness; deferred to integration test phase.

package handler

import "testing"

func TestIsVelafiAllowedDomain(t *testing.T) {
	tests := []struct {
		email string
		want  bool
	}{
		// allowed
		{"violin.wang@velafi.com", true},
		{"asher.dong@galactic.holdings", true},
		{"new.hire@velafi.com", true},

		// rejected: outside Velafi domains
		{"someone@gmail.com", false},
		{"hacker@multica.ai", false},
		{"foo@bar.com", false},

		// rejected: malformed
		{"", false},
		{"noatsign", false},
		{"@velafi.com", true},      // empty local part still has allowed domain → counted as allowed (caller checks empty separately)
		{"trailing@", false},       // missing domain
		{"trailing@.", false},      // dot-only domain
		{"@", false},               // just @
	}
	for _, tt := range tests {
		got := isVelafiAllowedDomain(tt.email)
		if got != tt.want {
			t.Errorf("isVelafiAllowedDomain(%q) = %v, want %v", tt.email, got, tt.want)
		}
	}
}

func TestLocalPartOf(t *testing.T) {
	tests := []struct {
		email string
		want  string
	}{
		{"violin.wang@galactic.holdings", "violin.wang"},
		{"asher.dong@velafi.com", "asher.dong"},
		{"plain.string", "plain.string"},
		{"@no-local", "@no-local"},
		{"", ""},
	}
	for _, tt := range tests {
		got := localPartOf(tt.email)
		if got != tt.want {
			t.Errorf("localPartOf(%q) = %q, want %q", tt.email, got, tt.want)
		}
	}
}
