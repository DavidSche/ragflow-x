package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// Trial runs have their own resource (doc/244): reading requires
// `trial-run:read`, starting or cancelling requires `trial-run:execute`.
//
// v0.4.0 borrowed `eval-set:execute` for the write path, which forced a choice
// between "team administrators cannot practise a template" and "team
// administrators may rewrite evidence snapshots" (that action also guards
// POST /eval-cases/:id/revalidate). The dedicated action breaks the coupling:
// practising is authoring, revalidating evidence is governance.
func authorizeTrialRunWrite(c *gin.Context) error {
	return authorizeResource(c, "trial-run", "execute")
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
	if err := authorizeResource(c, "trial-run", "read"); err != nil {
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
	if err := authorizeResource(c, "trial-run", "read"); err != nil {
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
