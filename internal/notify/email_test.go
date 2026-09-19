package notify

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestEmailNotifierBuildsSMTPMessageAndRetries(t *testing.T) {
	attempts := 0
	addresses := make([]string, 0, 2)
	notifier := NewEmail("email", config.Email{
		Host: "smtp.example.test", Port: 587, From: "alerts@example.test",
		To: "a@example.test, b@example.test", TimeoutSec: 1, MaxAttempts: 2,
		BackoffInitialMs: 0, BackoffMaxMs: 0,
	}, func(n *EmailNotifier) {
		n.sendMail = func(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
			attempts++
			addresses = append(addresses, addr)
			if attempts == 1 {
				return errors.New("transient smtp failure")
			}
			if from != "alerts@example.test" || len(to) != 2 {
				t.Fatalf("invalid envelope from=%q to=%q", from, to)
			}
			message := string(msg)
			for _, expected := range []string{"From: alerts@example.test", "To: a@example.test, b@example.test", "MIME-Version: 1.0"} {
				if !strings.Contains(message, expected) {
					t.Fatalf("message missing %q: %q", expected, message)
				}
			}
			return nil
		}
	})
	err := notifier.Send(context.Background(), Event{Title: "release rolled back", Type: "release.rollback", TenantID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(addresses) != 2 || addresses[0] != "smtp.example.test:587" {
		t.Fatalf("attempts=%d addresses=%v", attempts, addresses)
	}
}

func TestEmailNotifierValidatesConfiguration(t *testing.T) {
	notifier := NewEmail("email", config.Email{Enabled: true})
	err := notifier.Send(context.Background(), Event{ID: "alert-1"})
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.Attempts != 0 {
		t.Fatalf("expected immediate delivery error, got %v", err)
	}
}
