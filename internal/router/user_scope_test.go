package router_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPlatformAdminListUsersAcrossWorkspaces(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	tenants := map[string]string{}
	for _, name := range []string{"workspace-a", "workspace-b"} {
		resp, body := app.doAuth(t, http.MethodPost, "/api/v1/tenants", token, []byte(`{"name":"`+name+`"}`))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create tenant %s: %d %s", name, resp.StatusCode, body)
		}
		var tenant struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &tenant); err != nil {
			t.Fatal(err)
		}
		tenants[name] = tenant.Data.ID
	}

	for workspace, tenantID := range tenants {
		username := "user-" + workspace
		payload := `{"username":"` + username + `","password":"secret123","role":"operator","tenant_id":"` + tenantID + `"}`
		resp, body := app.doAuth(t, http.MethodPost, "/api/v1/users", token, []byte(payload))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create user %s: %d %s", username, resp.StatusCode, body)
		}
	}

	resp, body := app.doAuth(t, http.MethodGet, "/api/v1/users?scope=all", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list users: %d %s", resp.StatusCode, body)
	}
	var users struct {
		Data struct {
			Items []struct {
				Username   string `json:"username"`
				TenantID   string `json:"tenant_id"`
				TenantName string `json:"tenant_name"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, user := range users.Data.Items {
		for workspace, tenantID := range tenants {
			if user.Username == "user-"+workspace && user.TenantID == tenantID && user.TenantName != "" {
				found[workspace] = true
			}
		}
	}
	if len(found) != len(tenants) {
		t.Fatalf("expected users from every workspace, got %v in %+v", found, users.Data.Items)
	}
}
