// Package response provides a uniform API response envelope.
package response

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Body is the standard response envelope.
type Body struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	TraceID string      `json:"trace_id,omitempty"`
}

// OK writes a successful response.
func OK(c *gin.Context, data interface{}) {
	payload, err := json.Marshal(Body{Code: 0, Message: "ok", Data: data, TraceID: traceID(c)})
	if err != nil {
		fallback, _ := json.Marshal(Body{Code: 50000, Message: "response serialization failed", TraceID: traceID(c)})
		c.Data(http.StatusInternalServerError, "application/json; charset=utf-8", fallback)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
}

// Page wraps a paginated payload.
type Page struct {
	Items    interface{} `json:"items"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// OKPage writes a paginated successful response.
func OKPage(c *gin.Context, items interface{}, total int64, page, pageSize int) {
	OK(c, Page{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// Fail writes an error response.
func Fail(c *gin.Context, status int, code int, message string) {
	c.JSON(status, Body{Code: code, Message: message, TraceID: traceID(c)})
}

func traceID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetString("request_id")
}
