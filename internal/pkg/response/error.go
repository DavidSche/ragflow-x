package response

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Err renders a uniform error response. It recognizes httperr.Error and falls
// back to a generic 500 for unexpected errors.
func Err(c *gin.Context, err error) {
	var he *httperr.Error
	if errors.As(err, &he) {
		if he.Data != nil {
			// Structured error context (`data`) is part of the error envelope, not
			// an extra field on a special response: a client that only knows
			// {code,message} still reads the same two fields.
			c.JSON(he.Status, Body{Code: he.Code, Message: he.Message, Data: he.Data, TraceID: traceID(c)})
			return
		}
		Fail(c, he.Status, he.Code, he.Message)
		return
	}
	logger.Error("internal handler error", "err", err, "path", c.Request.URL.Path, "request_id", c.GetString("request_id"))
	Fail(c, 500, 500, "internal server error")
}
