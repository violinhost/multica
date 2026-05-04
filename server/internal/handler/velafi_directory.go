// velafi_directory.go — Velafi-fork-only handler that exposes the Lark
// tenant directory (synced from agentrunner2's roster.json dump) as a
// search endpoint for the velafi-quick-add autocomplete UI.
//
// Roster is embedded via go:embed at build time. Updates require a
// rebuild — fine for the new-hire cadence (rare). Future iteration may
// switch to a live mount or a periodic sync job.

package handler

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
)

//go:embed velafi_data/roster.json
var velafiRosterRaw []byte

// VelafiRosterEntry mirrors the JSON shape produced by the agentrunner2
// dump script (`walk_depts.py`). Only the fields the directory search
// needs are pulled out — the rest stay in the underlying JSON.
type VelafiRosterEntry struct {
	Name        string `json:"name"`
	EnName      string `json:"en_name,omitempty"`
	Email       string `json:"email"`
	OpenID      string `json:"open_id,omitempty"`
	UnionID     string `json:"union_id,omitempty"`
	JobTitle    string `json:"job_title,omitempty"`
	IsActivated bool   `json:"is_activated,omitempty"`
}

type velafiRosterFile struct {
	GeneratedAt string              `json:"generated_at"`
	AppID       string              `json:"app_id"`
	Count       int                 `json:"count"`
	Users       []VelafiRosterEntry `json:"users"`
}

var (
	rosterOnce  sync.Once
	rosterUsers []VelafiRosterEntry
	rosterErr   error
)

// loadRoster parses the embedded roster JSON exactly once. Subsequent
// calls reuse the parsed slice.
func loadRoster() ([]VelafiRosterEntry, error) {
	rosterOnce.Do(func() {
		var f velafiRosterFile
		if err := json.Unmarshal(velafiRosterRaw, &f); err != nil {
			rosterErr = err
			return
		}
		rosterUsers = f.Users
	})
	return rosterUsers, rosterErr
}

// VelafiDirectorySearchEntry is the response shape — slimmer than the
// full roster entry, matches what the autocomplete UI needs.
type VelafiDirectorySearchEntry struct {
	Name          string `json:"name"`
	Email         string `json:"email"`
	JobTitle      string `json:"job_title,omitempty"`
	AlreadyMember bool   `json:"already_member"`
}

type VelafiDirectorySearchResponse struct {
	Results []VelafiDirectorySearchEntry `json:"results"`
}

// VelafiDirectorySearch — GET /api/velafi/directory/search?q=<query>
//
// Searches the embedded Velafi tenant roster (case-insensitive substring
// match against name and email). Returns up to 20 results sorted by
// best-match heuristic (prefix > substring; activated > not).
//
// Caller does NOT have to be admin — directory lookup is read-only and
// returns only public-ish fields (name/email/job title). The auth
// requirement is "logged-in user", which middleware already enforces.
//
// Optionally takes ?workspace_id=<uuid> to mark which entries are
// already members of that workspace (so UI can grey them out).
func (h *Handler) VelafiDirectorySearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}

	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	roster, err := loadRoster()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load directory")
		return
	}

	// Score each entry; >0 means matched.
	type scored struct {
		entry VelafiRosterEntry
		score int
	}
	scoredHits := make([]scored, 0, 20)

	for _, u := range roster {
		s := matchScore(u, q)
		if s > 0 {
			scoredHits = append(scoredHits, scored{u, s})
		}
	}

	// Sort by score desc, then name asc.
	sort.Slice(scoredHits, func(i, j int) bool {
		if scoredHits[i].score != scoredHits[j].score {
			return scoredHits[i].score > scoredHits[j].score
		}
		return scoredHits[i].entry.Name < scoredHits[j].entry.Name
	})

	// Cap at 100 results — the Velafi tenant is small enough that the
	// frontend can hold all roster entries in memory and filter client-side
	// for autocomplete latency. Backend filtering still applies when a
	// query is given so very-large tenants don't blow up the response.
	if len(scoredHits) > 100 {
		scoredHits = scoredHits[:100]
	}

	// Optional: mark already-members for the given workspace.
	wsID := r.URL.Query().Get("workspace_id")
	memberEmails := map[string]bool{}
	if wsID != "" {
		wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
		if !ok {
			return
		}
		members, err := h.Queries.ListMembersWithUser(r.Context(), wsUUID)
		if err == nil {
			for _, m := range members {
				memberEmails[strings.ToLower(m.UserEmail)] = true
			}
		}
	}

	out := VelafiDirectorySearchResponse{Results: make([]VelafiDirectorySearchEntry, 0, len(scoredHits))}
	for _, h := range scoredHits {
		entry := VelafiDirectorySearchEntry{
			Name:          h.entry.Name,
			Email:         h.entry.Email,
			JobTitle:      h.entry.JobTitle,
			AlreadyMember: memberEmails[strings.ToLower(h.entry.Email)],
		}
		out.Results = append(out.Results, entry)
	}

	writeJSON(w, http.StatusOK, out)
}

// matchScore returns 0 if no match, higher value = better match.
//
// Scoring:
//   - 100: exact email match (case-insensitive)
//   - 50: name starts with query
//   - 30: email starts with query
//   - 10: name contains query as substring
//   - 5:  email contains query as substring
//   - 1:  job title contains query
//
// Empty query matches everything with score 1 — used for "show all"
// from the autocomplete dropdown when input is empty.
func matchScore(u VelafiRosterEntry, q string) int {
	if u.Email == "" {
		return 0
	}
	if q == "" {
		return 1
	}

	name := strings.ToLower(u.Name)
	enName := strings.ToLower(u.EnName)
	email := strings.ToLower(u.Email)
	job := strings.ToLower(u.JobTitle)

	if email == q {
		return 100
	}
	if strings.HasPrefix(name, q) || strings.HasPrefix(enName, q) {
		return 50
	}
	if strings.HasPrefix(email, q) {
		return 30
	}
	if strings.Contains(name, q) || strings.Contains(enName, q) {
		return 10
	}
	if strings.Contains(email, q) {
		return 5
	}
	if strings.Contains(job, q) {
		return 1
	}
	return 0
}
