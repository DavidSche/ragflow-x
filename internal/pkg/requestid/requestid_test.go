package requestid

import (
	"context"
	"testing"
)

func TestRequestIDContext(t *testing.T) {
	if got := FromContext(context.Background()); got != "" {
		t.Fatalf("empty context returned %q", got)
	}
	if got := FromContext(NewContext(context.Background(), "request-1")); got != "request-1" {
		t.Fatalf("FromContext = %q, want request-1", got)
	}
	if NewContext(context.Background(), "") != context.Background() {
		t.Fatal("empty request id should not create a new context")
	}
}
