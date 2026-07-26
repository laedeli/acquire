package httpapi

import (
	"context"
	"testing"
)

// Regression: a validly-signed token that happens to carry NO realm roles used
// to be treated as an admin, because the dev-bypass was detected by
// len(roles)==0. Only the explicit no-issuer bypass may grant a role.
func TestEmptyRoleListIsNotAdmin(t *testing.T) {
	ctx := context.WithValue(context.Background(), roleKey, []string{})
	if hasRole(ctx, "zaentrum-admin") {
		t.Fatal("a token with zero realm roles must not be admin")
	}
}

func TestRoleMatch(t *testing.T) {
	ctx := context.WithValue(context.Background(), roleKey, []string{"zaentrum-user", "zaentrum-admin"})
	if !hasRole(ctx, "zaentrum-admin") {
		t.Error("admin role should match")
	}
	if hasRole(ctx, "some-other-role") {
		t.Error("unrelated role must not match")
	}
}

func TestNoRolesAtAllIsNotAdmin(t *testing.T) {
	if hasRole(context.Background(), "zaentrum-admin") {
		t.Fatal("a request with no role context must not be admin")
	}
}

// The dev bypass (no OIDC_ISSUER configured) still grants access locally.
func TestDevBypassGrantsRole(t *testing.T) {
	ctx := context.WithValue(context.Background(), devBypassCtxKey{}, true)
	if !hasRole(ctx, "zaentrum-admin") {
		t.Fatal("auth-disabled dev path should allow")
	}
}
