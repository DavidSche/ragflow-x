package ragflow

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClient_CreateAgentSessionContractVariants(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"object id", `{"id":"s1","name":"object"}`},
		{"session id", `{"session_id":"s2","name":"session"}`},
		{"array", `[{"id":"s3","name":"array"}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":0,"data":` + test.data + `}`))
			}))
			defer srv.Close()
			client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
			session, err := client.CreateAgentSession(t.Context(), "agent", "test")
			if err != nil {
				t.Fatal(err)
			}
			if session.ID == "" {
				t.Fatal("session ID is empty")
			}
		})
	}
}
