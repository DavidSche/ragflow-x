package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// Trial runs reuse the existing grant matrix instead of introducing a new
// resource: creating/cancelling requires eval-set execute (tenant_admin,
// operator) and reading requires scenario-template read. Adding a dedicated
// permission row would change the 423-grant matrix, which is out of scope for
// v0.4.0 (doc/228 §4.3).
func authorizeTrialRunWrite(c *gin.Context) error {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		return err
	}
	return authorizeResource(c, "eval-set", "execute")
}

func createTrialRun(c *gin.Context) {
	if err := authorizeTrialRunWrite(c); err != nil {
		response.Err(c, err)
		return
	}
	var in service.TrialRunInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Fail(c, 400, 40123, "invalid trial run payload")
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.CreateTrialRun(c.Request.Context(), tenantID, userID, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

func getTrialRun(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.GetTrialRun(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

func listTrialRuns(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	in := service.TrialRunInput{
		ScenarioTemplateID: c.Query("scenario_template_id"),
		AssistantID:        c.Query("assistant_id"),
	}
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.ListTrialRuns(c.Request.Context(), tenantID, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": out})
}

func cancelTrialRun(c *gin.Context) {
	if err := authorizeTrialRunWrite(c); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.CancelTrialRun(c.Request.Context(), tenantID, userID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

// onboardingReadiness exposes the derived four-milestone readiness projection
// (doc/228 §3.1 T7.1′). Read-only and tenant-local.
func onboardingReadiness(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.OnboardingReadiness(c.Request.Context(), tenantID, c.Query("assistant_id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}
