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
	"sort"
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
	ErrIdempotencyConflict = errors.New("ci repository-profile idempotency conflict")
	ErrNotFound            = errors.New("ci repository-profile not found")
	ErrStoreUnavailable    = errors.New("ci repository-profile store unavailable")
	ErrVerifierUnavailable = errors.New("ci repository-profile verifier unavailable")
	ErrInvalidAttestation  = errors.New("ci repository-profile attestation rejected")
	ErrRepositoryMismatch  = errors.New("ci repository-profile repository identity mismatch")
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
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Registration{}, err
	}
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

// DecodeEnableAttestation accepts exactly the small enable wire shape. The
// opaque payload remains opaque to the HTTP handler but is still checked for
// duplicate keys so two parsers cannot disagree about its meaning.
func DecodeEnableAttestation(data []byte) (json.RawMessage, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, err
	}
	var request struct {
		AdapterAttestation json.RawMessage `json:"adapter_attestation"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil || len(request.AdapterAttestation) == 0 || !json.Valid(request.AdapterAttestation) {
		return nil, fmt.Errorf("%w: adapter_attestation is required", ErrInvalidRequest)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: request must contain one JSON object", ErrInvalidRequest)
	}
	return request.AdapterAttestation, nil
}

// DecodeDisableRequest permits exactly one empty JSON object. Disable has no
// caller-controlled lifecycle input, so accepting ignored fields is unsafe.
func DecodeDisableRequest(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var request map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&request); err != nil || request == nil || len(request) != 0 {
		return fmt.Errorf("%w: disable request must be an empty JSON object", ErrInvalidRequest)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request must contain one JSON object", ErrInvalidRequest)
	}
	return nil
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

// RequestDigest binds an idempotency key to the complete immutable request.
// The opaque attestation is normalized and hashed, never persisted.
func RequestDigest(repository string, r Registration) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	attestation, err := CanonicalJSON(r.AdapterAttestation)
	if err != nil {
		return "", err
	}
	r.AdapterAttestation = attestation
	payload := struct {
		Repository   string `json:"repository"`
		Registration `json:"registration"`
	}{Repository: repository, Registration: r}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// CanonicalJSON preserves JSON number tokens losslessly while sorting object
// keys. It makes whitespace and key ordering irrelevant without converting
// large integers through float64.
func CanonicalJSON(data []byte) ([]byte, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON", ErrInvalidRequest)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: request must contain one JSON value", ErrInvalidRequest)
	}
	var out bytes.Buffer
	if err := appendCanonicalJSON(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func appendCanonicalJSON(out *bytes.Buffer, value any) error {
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			out.Write(encoded)
			out.WriteByte(':')
			if err := appendCanonicalJSON(out, value[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := appendCanonicalJSON(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case json.Number:
		out.WriteString(value.String())
	case string, bool, nil:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out.Write(encoded)
	default:
		return fmt.Errorf("%w: unsupported JSON value", ErrInvalidRequest)
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSONValue(dec); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request must contain one JSON value", ErrInvalidRequest)
	}
	return nil
}

func walkJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]struct{})
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key must be a string")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			keys[key] = struct{}{}
			if err := walkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := walkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
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
