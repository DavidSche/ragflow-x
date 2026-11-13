package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestUpstreamWriteFailureSurfacesEngineReason pins the drill lesson: an engine
// that answers HTTP 200 + non-zero code has deliberately refused the request, so
// the caller gets 400/40085 with the reason instead of a 502 that hides it.
func TestUpstreamWriteFailureSurfacesEngineReason(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		code  int
		want  string
		check func(*testing.T, error)
	}{
		{
			name: "dataset model misconfiguration",
			err:  &ragflow.Error{HTTPStatus: 200, Code: 500, Message: `model configuration unavailable: model "0" has no saved model row`},
			code: 40085,
			want: "ragflow rejected dataset creation: model configuration unavailable",
		},
		{
			name: "chat over unparsed dataset",
			err:  &ragflow.Error{HTTPStatus: 200, Code: 102, Message: "the dataset db0 doesn't own parsed file"},
			code: 40085,
			want: "ragflow rejected chat creation: the dataset db0 doesn't own parsed file",
		},
		{
			name: "transport failure stays a gateway error",
			err:  fmt.Errorf("dial tcp: connection refused"),
			code: 50240,
			want: "ragflow chat creation failed",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			action := "chat creation"
			gateway := 50240
			if testCase.name == "dataset model misconfiguration" {
				action, gateway = "dataset creation", 50200
			}
			err := upstreamWriteFailure(testCase.err, action, gateway)
			var apiErr *httperr.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected a typed api error, got %T", err)
			}
			if apiErr.Code != testCase.code {
				t.Fatalf("code=%d, want %d (%v)", apiErr.Code, testCase.code, err)
			}
			if testCase.want != "" && !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("message %q must contain %q", err.Error(), testCase.want)
			}
		})
	}
}

// TestUpstreamRejectionTruncatesLongReasons keeps the response bounded: an engine
// may echo a whole stack trace, and no client needs more than a sentence.
func TestUpstreamRejectionTruncatesLongReasons(t *testing.T) {
	long := make([]byte, 400)
	for i := range long {
		long[i] = 'x'
	}
	reason, ok := upstreamRejection(&ragflow.Error{HTTPStatus: 200, Code: 102, Message: string(long)})
	if !ok {
		t.Fatal("a business rejection must be recognised")
	}
	if len(reason) != upstreamReasonLimit {
		t.Fatalf("reason length=%d, want %d", len(reason), upstreamReasonLimit)
	}
}
