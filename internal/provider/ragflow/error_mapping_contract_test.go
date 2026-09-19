package ragflow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_HTTPStatusesMapToStableErrorTypes(t *testing.T) {
	cases := []struct {
		status        int
		wantType      ErrorType
		wantRetryable bool
	}{
		{http.StatusBadRequest, ErrorTypeBusiness, false},
		{http.StatusUnauthorized, ErrorTypeTransport, false},
		{http.StatusForbidden, ErrorTypeTransport, false},
		{http.StatusNotFound, ErrorTypeBusiness, false},
		{http.StatusConflict, ErrorTypeBusiness, false},
		{http.StatusRequestTimeout, ErrorTypeTimeout, true},
		{http.StatusTooManyRequests, ErrorTypeTransport, true},
		{http.StatusInternalServerError, ErrorTypeTransport, true},
		{http.StatusBadGateway, ErrorTypeTransport, true},
	}

	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"code":0,"message":"upstream rejected","data":null}`))
			}))
			defer srv.Close()

			client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
			_, err := client.CreateDataset(context.Background(), CreateDatasetRequest{Name: "contract"})
			if err == nil {
				t.Fatal("expected non-2xx response to fail")
			}
			var providerErr *Error
			if !errors.As(err, &providerErr) {
				t.Fatalf("expected *Error, got %T: %v", err, err)
			}
			if providerErr.HTTPStatus != tc.status || providerErr.Type != tc.wantType || providerErr.Message != "upstream rejected" {
				t.Fatalf("unexpected error: %+v", providerErr)
			}
			if providerErr.IsRetryable() != tc.wantRetryable {
				t.Fatalf("retryable = %v, want %v", providerErr.IsRetryable(), tc.wantRetryable)
			}
		})
	}
}
