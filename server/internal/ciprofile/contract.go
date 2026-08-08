// Package ciprofile defines the fixed, fail-closed repository-profile contract.
package ciprofile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

const (
	SchemaVersion   = "ci.repository-profile.v1"
	WorkflowClass   = "native_ci"
	WorkflowVersion = "v1"
	JobClass        = "backend"
	CheckName       = "backend"
)

var (
	ErrInvalidRequest      = errors.New("invalid ci repository-profile request")
	ErrVerifierUnavailable = errors.New("ci repository-profile verifier unavailable")
	ErrInvalidAttestation  = errors.New("ci repository-profile attestation rejected")
	shaPattern             = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Registration is the sole accepted registration shape. The server binds the
// selected resource to its URL and never accepts a repository or checkout path
// from this request.
type Registration struct {
	SchemaVersion      string          `json:"schema_version"`
	RequestID          string          `json:"request_id"`
	WorkspaceID        string          `json:"workspace_id"`
	ProjectID          string          `json:"project_id"`
	ResourceID         string          `json:"resource_id"`
	Revision           string          `json:"revision"`
	WorkflowClass      string          `json:"workflow_class"`
	WorkflowVersion    string          `json:"workflow_version"`
	JobClass           string          `json:"job_class"`
	CheckName          string          `json:"check_name"`
	ServiceClasses     []string        `json:"service_classes"`
	HostedFallback     bool            `json:"hosted_fallback"`
	AdapterAttestation json.RawMessage `json:"adapter_attestation"`
	Source             string          `json:"source"`
}

// DecodeRegistration rejects duplicate JSON values, unknown fields, and
// trailing input. This keeps the wire ABI narrow before any mutation occurs.
func DecodeRegistration(data []byte) (Registration, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var request Registration
	if err := dec.Decode(&request); err != nil {
		return Registration{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Registration{}, fmt.Errorf("%w: request must contain one JSON object", ErrInvalidRequest)
	}
	if err := request.Validate(); err != nil {
		return Registration{}, err
	}
	return request, nil
}

func (r Registration) Validate() error {
	if r.SchemaVersion != SchemaVersion || strings.TrimSpace(r.RequestID) == "" || strings.TrimSpace(r.WorkspaceID) == "" || strings.TrimSpace(r.ProjectID) == "" || strings.TrimSpace(r.ResourceID) == "" || strings.TrimSpace(r.Source) == "" {
		return fmt.Errorf("%w: schema_version, request_id, workspace_id, project_id, resource_id, and source are required", ErrInvalidRequest)
	}
	if !ValidRevision(r.Revision) {
		return fmt.Errorf("%w: revision must be 40 lowercase hexadecimal characters", ErrInvalidRequest)
	}
	if r.WorkflowClass != WorkflowClass || r.WorkflowVersion != WorkflowVersion || r.JobClass != JobClass || r.CheckName != CheckName || r.HostedFallback {
		return fmt.Errorf("%w: unsupported workflow declaration", ErrInvalidRequest)
	}
	if len(r.ServiceClasses) != 2 || r.ServiceClasses[0] != "postgresql_pgvector" || r.ServiceClasses[1] != "redis" {
		return fmt.Errorf("%w: service_classes must be [postgresql_pgvector redis]", ErrInvalidRequest)
	}
	if len(r.AdapterAttestation) == 0 || !json.Valid(r.AdapterAttestation) {
		return fmt.Errorf("%w: adapter_attestation is required", ErrInvalidRequest)
	}
	return nil
}

// ValidRevision accepts only an immutable Git commit SHA in its canonical form.
func ValidRevision(revision string) bool { return shaPattern.MatchString(revision) }

// CanonicalRepositoryIdentity accepts only GitHub repository URLs and returns
// the stable owner/repository identity. It normalizes HTTPS, ssh:// and the
// scp-like git@github.com form without trusting a caller-selected repo string.
func CanonicalRepositoryIdentity(resourceURL string) (string, error) {
	s := strings.TrimSpace(resourceURL)
	if strings.HasPrefix(s, "git@github.com:") {
		s = "ssh://git@github.com/" + strings.TrimPrefix(s, "git@github.com:")
	}
	u, err := url.Parse(s)
	if err != nil || u == nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", fmt.Errorf("%w: resource must be a github.com repository URL", ErrInvalidRequest)
	}
	path := strings.TrimSuffix(strings.Trim(u.EscapedPath(), "/"), ".git")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(parts[0], " ") || strings.Contains(parts[1], " ") {
		return "", fmt.Errorf("%w: malformed github repository URL", ErrInvalidRequest)
	}
	return strings.ToLower(parts[0] + "/" + parts[1]), nil
}

// ProjectionDigest binds every verifier decision to immutable profile input.
func ProjectionDigest(repository string, r Registration) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	payload := struct {
		Repository      string   `json:"repository"`
		SchemaVersion   string   `json:"schema_version"`
		Revision        string   `json:"revision"`
		WorkflowClass   string   `json:"workflow_class"`
		WorkflowVersion string   `json:"workflow_version"`
		JobClass        string   `json:"job_class"`
		CheckName       string   `json:"check_name"`
		ServiceClasses  []string `json:"service_classes"`
		HostedFallback  bool     `json:"hosted_fallback"`
	}{repository, r.SchemaVersion, r.Revision, r.WorkflowClass, r.WorkflowVersion, r.JobClass, r.CheckName, r.ServiceClasses, r.HostedFallback}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Verifier is intentionally small. Production wiring can remain unavailable;
// callers must fail closed rather than treating pending evidence as enabled.
type Verifier interface {
	Verify(context.Context, Evidence) (Attestation, error)
}

type Evidence struct {
	ProfileID        string
	Generation       int
	Revision         string
	ProjectionDigest string
	OpaqueInput      json.RawMessage
}

type Attestation struct {
	ProfileID        string
	Generation       int
	Revision         string
	ProjectionDigest string
	Reference        string
}

func VerifyEnable(ctx context.Context, verifier Verifier, evidence Evidence) (Attestation, error) {
	if verifier == nil {
		return Attestation{}, ErrVerifierUnavailable
	}
	attestation, err := verifier.Verify(ctx, evidence)
	if err != nil || attestation.ProfileID != evidence.ProfileID || attestation.Generation != evidence.Generation || attestation.Revision != evidence.Revision || attestation.ProjectionDigest != evidence.ProjectionDigest || strings.TrimSpace(attestation.Reference) == "" {
		return Attestation{}, ErrInvalidAttestation
	}
	return attestation, nil
}
