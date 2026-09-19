package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type webhookFormat int

const (
	webhookFormatGeneric webhookFormat = iota
	webhookFormatWeCom
	webhookFormatDingTalk
)

type enterpriseRobotError struct {
	Code    int
	Message string
}

func (e *enterpriseRobotError) Error() string {
	return fmt.Sprintf("enterprise robot returned errcode %d: %s", e.Code, e.Message)
}

func normalizeChannelType(value string) webhookFormat {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "wecom", "wechat-work":
		return webhookFormatWeCom
	case "dingtalk":
		return webhookFormatDingTalk
	default:
		return webhookFormatGeneric
	}
}

func (w *WebhookNotifier) payload(ev Event) any {
	switch w.format {
	case webhookFormatWeCom:
		return map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"content": formatChannelText(ev, 1800)},
		}
	case webhookFormatDingTalk:
		return map[string]any{
			"msgtype": "markdown",
			"markdown": map[string]any{
				"title": sanitizeHeader(ev.Title),
				"text":  formatChannelText(ev, 8000),
			},
		}
	default:
		return ev.payload()
	}
}

func formatChannelText(ev Event, maxRunes int) string {
	title := strings.TrimSpace(ev.Title)
	if title == "" {
		title = strings.TrimSpace(ev.Type)
	}
	lines := []string{
		"**" + title + "**",
		"**Severity:** " + strings.TrimSpace(ev.Severity),
	}
	if ev.Type != "" {
		lines = append(lines, "Type: `"+strings.TrimSpace(ev.Type)+"`")
	}
	if ev.TenantID != "" {
		lines = append(lines, "Tenant: "+ev.TenantID)
	}
	if ev.Resource != "" {
		resource := ev.Resource
		if ev.ResourceID != "" {
			resource += "/" + ev.ResourceID
		}
		lines = append(lines, "Resource: "+resource)
	}
	if detail := strings.TrimSpace(ev.Detail); detail != "" {
		lines = append(lines, "", detail)
	}
	if !ev.OccurredAt.IsZero() {
		lines = append(lines, "", "Occurred: "+ev.OccurredAt.UTC().Format(time.RFC3339))
	}
	return truncateRunes(strings.Join(lines, "\n"), maxRunes)
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}

func (w *WebhookNotifier) signedDingTalkURL() string {
	if w.secret == "" {
		return w.url
	}
	parsed, err := url.Parse(w.url)
	if err != nil {
		return w.url
	}
	timestamp := time.Now().UnixMilli()
	stringToSign := strconv.FormatInt(timestamp, 10) + "\n" + w.secret
	mac := hmac.New(sha256.New, []byte(w.secret))
	_, _ = mac.Write([]byte(stringToSign))
	query := parsed.Query()
	query.Set("timestamp", strconv.FormatInt(timestamp, 10))
	query.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func checkEnterpriseRobotResponse(response []byte) error {
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return errors.New("enterprise robot response is not JSON")
	}
	if result.ErrCode != 0 {
		return &enterpriseRobotError{Code: result.ErrCode, Message: result.ErrMsg}
	}
	return nil
}
