package performance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPProbe describes one endpoint request. The runner intentionally checks
// the exact expected status and a substring so a 429/503 page is never counted
// as success merely because HTTP latency was available.
type HTTPProbe struct {
	URL           string
	Method        string
	Headers       map[string]string
	Body          string
	ExpectedCode  int
	ExpectedBody  string
	Stream        bool
	IgnoreContent bool
}

// HTTPOperation converts a probe into a sampler Operation. For streams it
// records time-to-first-byte and drains the complete response before returning.
func HTTPOperation(client *http.Client, probe HTTPProbe) Operation {
	return func(ctx context.Context) (Attempt, error) {
		attempt := Attempt{}
		req, err := http.NewRequestWithContext(ctx, probe.Method, probe.URL, strings.NewReader(probe.Body))
		if err != nil {
			return attempt, err
		}
		for key, value := range probe.Headers {
			req.Header.Set(key, value)
		}
		if probe.Body != "" && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		started := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return attempt, err
		}
		defer resp.Body.Close()
		if probe.Stream {
			firstByte := time.Since(started)
			if firstByte <= 0 {
				firstByte = time.Nanosecond
			}
			attempt.TTFT = &firstByte
		}
		if resp.StatusCode != probe.ExpectedCode {
			return attempt, fmt.Errorf("http status %d", resp.StatusCode)
		}
		if probe.Stream {
			scanner := bufio.NewScanner(resp.Body)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				if bytes.Contains(scanner.Bytes(), []byte("data: [DONE]")) {
					break
				}
			}
			if err := scanner.Err(); err != nil {
				return attempt, err
			}
			return attempt, nil
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		if err != nil {
			return attempt, err
		}
		if !probe.IgnoreContent && probe.ExpectedBody != "" && !bytes.Contains(body, []byte(probe.ExpectedBody)) {
			return attempt, fmt.Errorf("unexpected response content: %s", truncate(string(body), 160))
		}
		return attempt, nil
	}
}

// GenericJSONProbe builds a non-streaming HTTP probe with the common headers.
func GenericJSONProbe(method, url, body string, expectedCode int, expectedBody string) HTTPProbe {
	return HTTPProbe{
		URL: url, Method: method, Body: body,
		ExpectedCode: expectedCode, ExpectedBody: expectedBody,
	}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

// JSONAttempt decodes the response into an OpenAI-compatible non-stream body.
type JSONAttempt struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// ParseJSON validates that a non-stream response is usable.
func ParseJSON(body []byte) (JSONAttempt, error) {
	var parsed JSONAttempt
	if err := json.Unmarshal(body, &parsed); err != nil {
		return parsed, err
	}
	if parsed.ID == "" || len(parsed.Choices) == 0 {
		return parsed, fmt.Errorf("invalid chat completion response")
	}
	return parsed, nil
}

// FirstSSEData extracts the first non-empty data event duration from a stream
// body for cases where the caller has already buffered the response.
func FirstSSEData(body []byte) (string, bool) {
	for _, line := range strings.Split(string(body), "\n") {
		value, found := strings.CutPrefix(strings.TrimSpace(line), "data: ")
		if found && value != "[DONE]" {
			return value, true
		}
	}
	return "", false
}

// QuotaHeaders returns remaining quota headers for evidence in failure messages.
func QuotaHeaders(header http.Header) string {
	return "tokens=" + header.Get("X-Quota-Remaining") + ", requests=" +
		strconv.Quote(header.Get("X-Quota-Requests-Remaining"))
}
