package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

type sendMailFunc func(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error

// EmailNotifier delivers governance and alert events over SMTP. It requires
// STARTTLS so credentials and event payloads are not transmitted in cleartext.
type EmailNotifier struct {
	name           string
	host           string
	port           int
	username       string
	password       string
	from           string
	recipients     []string
	timeout        time.Duration
	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	sendMail       sendMailFunc
}

type EmailOption func(*EmailNotifier)

func NewEmail(name string, cfg config.Email, options ...EmailOption) *EmailNotifier {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = defaultWebhookTimeout
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultWebhookMaxAttempts
	}
	initialBackoff := time.Duration(cfg.BackoffInitialMs) * time.Millisecond
	if initialBackoff < 0 {
		initialBackoff = defaultWebhookInitialBackoff
	}
	maxBackoff := time.Duration(cfg.BackoffMaxMs) * time.Millisecond
	if maxBackoff < initialBackoff {
		maxBackoff = initialBackoff
	}
	recipients := make([]string, 0)
	for _, recipient := range strings.Split(cfg.To, ",") {
		recipient = strings.TrimSpace(recipient)
		if recipient != "" {
			recipients = append(recipients, recipient)
		}
	}
	result := &EmailNotifier{
		name: name, host: strings.TrimSpace(cfg.Host), port: cfg.Port,
		username: strings.TrimSpace(cfg.Username), password: cfg.Password,
		from: strings.TrimSpace(cfg.From), recipients: recipients,
		timeout: timeout, maxAttempts: maxAttempts,
		initialBackoff: initialBackoff, maxBackoff: maxBackoff,
		sendMail: sendSMTPMail,
	}
	for _, option := range options {
		option(result)
	}
	return result
}

func (n *EmailNotifier) Name() string { return n.name }

func (n *EmailNotifier) Send(ctx context.Context, ev Event) error {
	if n.host == "" || n.from == "" || len(n.recipients) == 0 {
		return &DeliveryError{Notifier: n.name, LastError: errors.New("email host, sender or recipient is missing")}
	}
	msg, err := n.message(ev)
	if err != nil {
		return &DeliveryError{Notifier: n.name, LastError: err}
	}
	var auth smtp.Auth
	if n.username != "" {
		auth = smtp.PlainAuth("", n.username, n.password, n.host)
	}
	for attempt := 1; attempt <= n.maxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, n.timeout)
		err := n.sendMail(attemptCtx, n.address(), auth, n.from, n.recipients, msg)
		cancel()
		if err == nil {
			return nil
		}
		retryable := ctx.Err() == nil && isRetryableWebhookError(err)
		if attempt == n.maxAttempts || !retryable {
			return &DeliveryError{Notifier: n.name, Attempts: attempt, Retryable: retryable, LastError: err}
		}
		timer := time.NewTimer(n.backoffDelay(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return &DeliveryError{Notifier: n.name, Attempts: attempt, LastError: ctx.Err()}
		case <-timer.C:
		}
	}
	return &DeliveryError{Notifier: n.name, LastError: errors.New("email delivery policy allows no attempts")}
}

func (n *EmailNotifier) address() string {
	if n.port <= 0 {
		return net.JoinHostPort(n.host, "587")
	}
	return net.JoinHostPort(n.host, strconv.Itoa(n.port))
}

func (n *EmailNotifier) backoffDelay(attempt int) time.Duration {
	shift := attempt - 1
	if shift > 4 {
		shift = 4
	}
	delay := n.initialBackoff << shift
	if delay > n.maxBackoff {
		return n.maxBackoff
	}
	return delay
}

func (n *EmailNotifier) message(ev Event) ([]byte, error) {
	body, err := json.Marshal(ev.payload())
	if err != nil {
		return nil, err
	}
	subject := sanitizeHeader(ev.Title)
	encodedSubject := base64.StdEncoding.EncodeToString([]byte(subject))
	header := []string{
		"From: " + n.from,
		"To: " + strings.Join(n.recipients, ", "),
		fmt.Sprintf("Subject: =?UTF-8?B?%s?=", encodedSubject),
		"MIME-Version: 1.0",
		"Content-Type: application/json; charset=UTF-8",
	}
	if !ev.OccurredAt.IsZero() {
		header = append(header, "Date: "+ev.OccurredAt.UTC().Format(time.RFC1123Z))
	}
	return []byte(strings.Join(header, "\r\n") + "\r\n\r\n" + string(body) + "\r\n"), nil
}

func sanitizeHeader(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func sendSMTPMail(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return errors.New("smtp server does not support STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
		return err
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
