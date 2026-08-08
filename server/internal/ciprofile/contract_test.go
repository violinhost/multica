package ciprofile

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func validRegistration() Registration {
	return Registration{SchemaVersion: SchemaVersion, RequestID: "req-1", WorkspaceID: "workspace", ProjectID: "project", ResourceID: "resource", Revision: "0123456789abcdef0123456789abcdef01234567", WorkflowClass: WorkflowClass, WorkflowVersion: WorkflowVersion, JobClass: JobClass, CheckName: CheckName, ServiceClasses: []string{"postgresql_pgvector", "redis"}, AdapterAttestation: json.RawMessage(`{"opaque":"input"}`), Source: "owner_admin"}
}

func TestDecodeRegistrationRejectsUnknownAndUnsafeFields(t *testing.T) {
	request := validRegistration()
	b, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{append(b[:len(b)-1], []byte(`,"runner":"privileged"}`)...), append(b, []byte(` {}`)...)} {
		if _, err := DecodeRegistration(body); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("DecodeRegistration() error = %v, want ErrInvalidRequest", err)
		}
	}
}

func TestDecodeRegistrationRejectsDuplicateFieldsAtEveryDepth(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"schema_version":"ci.repository-profile.v1","request_id":"first","request_id":"second","workspace_id":"workspace","project_id":"project","resource_id":"resource","revision":"0123456789abcdef0123456789abcdef01234567","workflow_class":"native_ci","workflow_version":"v1","job_class":"backend","check_name":"backend","service_classes":["postgresql_pgvector","redis"],"hosted_fallback":false,"adapter_attestation":{},"source":"owner_admin"}`),
		[]byte(`{"schema_version":"ci.repository-profile.v1","request_id":"req-1","workspace_id":"workspace","project_id":"project","resource_id":"resource","revision":"0123456789abcdef0123456789abcdef01234567","workflow_class":"native_ci","workflow_version":"v1","job_class":"backend","check_name":"backend","service_classes":["postgresql_pgvector","redis"],"hosted_fallback":false,"adapter_attestation":{"counter":1,"counter":2},"source":"owner_admin"}`),
	} {
		if _, err := DecodeRegistration(body); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("DecodeRegistration(%s) error = %v, want ErrInvalidRequest", body, err)
		}
	}
}

func TestRegistrationRejectsMutableOrNonCanonicalDeclaration(t *testing.T) {
	for _, mutate := range []func(*Registration){
		func(r *Registration) { r.Revision = "ABC" },
		func(r *Registration) { r.JobClass = "frontend" },
		func(r *Registration) { r.ServiceClasses = []string{"redis", "postgresql_pgvector"} },
		func(r *Registration) { r.HostedFallback = true },
	} {
		r := validRegistration()
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Validate() error = %v, want ErrInvalidRequest", err)
		}
	}
}

func TestCanonicalRepositoryIdentity(t *testing.T) {
	for _, raw := range []string{"https://github.com/ViolinHost/Multica.git", "ssh://git@github.com/ViolinHost/Multica.git", "git@github.com:ViolinHost/Multica.git"} {
		got, err := CanonicalRepositoryIdentity(raw)
		if err != nil || got != "violinhost/multica" {
			t.Fatalf("CanonicalRepositoryIdentity(%q) = %q, %v", raw, got, err)
		}
	}
	if _, err := CanonicalRepositoryIdentity("https://example.com/violinhost/multica"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-GitHub URL error = %v", err)
	}
}

func TestRequestDigestBindsIdempotencyPayloadButNormalizesOpaqueJSON(t *testing.T) {
	r := validRegistration()
	digest, err := RequestDigest("violinhost/multica", r)
	if err != nil {
		t.Fatal(err)
	}
	r.AdapterAttestation = json.RawMessage(`{ "opaque": "input" }`)
	if normalized, err := RequestDigest("violinhost/multica", r); err != nil || normalized != digest {
		t.Fatalf("normalized digest = %q, %v; want %q", normalized, err, digest)
	}
	r.Revision = "1123456789abcdef0123456789abcdef01234567"
	if changed, err := RequestDigest("violinhost/multica", r); err != nil || changed == digest {
		t.Fatalf("changed payload digest = %q, %v; want distinct", changed, err)
	}
}

func TestRequestDigestCanonicalizesOpaqueJSONWithoutFloat64Collisions(t *testing.T) {
	r := validRegistration()
	r.AdapterAttestation = json.RawMessage(`{"z":[{"b":2,"a":1}],"counter":9007199254740992}`)
	first, err := RequestDigest("violinhost/multica", r)
	if err != nil {
		t.Fatal(err)
	}
	r.AdapterAttestation = json.RawMessage(` { "counter" : 9007199254740992 , "z" : [ { "a" : 1 , "b" : 2 } ] } `)
	if reordered, err := RequestDigest("violinhost/multica", r); err != nil || reordered != first {
		t.Fatalf("reordered digest = %q, %v; want %q", reordered, err, first)
	}
	r.AdapterAttestation = json.RawMessage(`{"counter":9007199254740993,"z":[{"a":1,"b":2}]}`)
	if changed, err := RequestDigest("violinhost/multica", r); err != nil || changed == first {
		t.Fatalf("large integer digest = %q, %v; want distinct from %q", changed, err, first)
	}
	r.AdapterAttestation = json.RawMessage(`{"counter":1.0}`)
	decimal, err := RequestDigest("violinhost/multica", r)
	if err != nil {
		t.Fatal(err)
	}
	r.AdapterAttestation = json.RawMessage(`{"counter":1}`)
	integer, err := RequestDigest("violinhost/multica", r)
	if err != nil || decimal == integer {
		t.Fatalf("numeric spelling must remain lossless: decimal=%q integer=%q err=%v", decimal, integer, err)
	}
}

func TestDecodeDisableRequestAcceptsOnlyEmptyObject(t *testing.T) {
	if err := DecodeDisableRequest([]byte(`{}`)); err != nil {
		t.Fatalf("empty disable request: %v", err)
	}
	for _, body := range [][]byte{[]byte(`{"provider":"github-hosted"}`), []byte(`[]`), []byte(`{} {}`)} {
		if err := DecodeDisableRequest(body); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("DecodeDisableRequest(%s) error = %v, want ErrInvalidRequest", body, err)
		}
	}
}

type verifier struct{ attestation Attestation }

func (v verifier) Verify(context.Context, Evidence) (Attestation, error) { return v.attestation, nil }

func TestVerifyEnableFailsClosed(t *testing.T) {
	evidence := Evidence{ProfileID: "profile", Generation: 1, Revision: validRegistration().Revision, ProjectionDigest: "digest", OpaqueInput: json.RawMessage(`{}`)}
	if _, err := VerifyEnable(context.Background(), nil, evidence); !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("nil verifier error = %v", err)
	}
	if _, err := VerifyEnable(context.Background(), verifier{Attestation{ProfileID: "other"}}, evidence); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("mismatched verifier error = %v", err)
	}
	if _, err := VerifyEnable(context.Background(), verifier{Attestation{ProfileID: "profile", Generation: 1, Revision: evidence.Revision, ProjectionDigest: "digest", Reference: "opaque-ref"}}, evidence); err != nil {
		t.Fatalf("matching verifier error = %v", err)
	}
}
