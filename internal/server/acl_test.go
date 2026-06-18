package server

import (
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestACLPolicyApprovalTarget(t *testing.T) {
	if got := aclPolicyApprovalTarget(store.ACLPolicyRecord{ID: "acl-1"}); got != "acl-1" {
		t.Fatalf("target = %q, want acl-1", got)
	}
	got := aclPolicyApprovalTarget(store.ACLPolicyRecord{Scope: "team:example", Name: "allow-portal"})
	if got != "team:example/allow-portal" {
		t.Fatalf("target = %q, want team:example/allow-portal", got)
	}
}
