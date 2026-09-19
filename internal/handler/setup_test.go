package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/rsaseal"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestSetupAdminDecryptsEncryptedPassword(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "h.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("sec", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	kp, err := rsaseal.Ensure(filepath.Join(t.TempDir(), "rsa.pem"))
	if err != nil {
		t.Fatal(err)
	}
	h := New(svc)
	h.SetSetupRSA(kp)

	sealed, err := kp.Encrypt("strong-admin-pass")
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/system/setup/admin", h.SetupAdmin)

	body, _ := json.Marshal(map[string]string{"username": "root", "password_enc": sealed})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/system/setup/admin", bytes.NewReader(body))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	u, err := svc.Store.GetUserByUsername(context.Background(), "root")
	if err != nil || u == nil {
		t.Fatalf("first admin not created: %v", err)
	}
}
