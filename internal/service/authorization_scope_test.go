package service

import "testing"

func TestUserAllowedByScopeFailsClosed(t *testing.T) {
	if userAllowedByScope(`{"roles":["tenant_admin"]}`, "tenant_admin") != true {
		t.Fatal("expected valid scope role to be allowed")
	}
	if userAllowedByScope(`{"roles":["tenant_admin"],"extra":"unexpected"}`, "tenant_admin") {
		t.Fatal("expected scope with extra keys to fail closed")
	}
	if userAllowedByScope(`{"roles":["unknown_role"]}`, "unknown_role") {
		t.Fatal("expected unknown scope role to fail closed")
	}
	if userAllowedByScope(`{"roles":[]}`, "tenant_admin") {
		t.Fatal("expected empty scope to fail closed")
	}
}
