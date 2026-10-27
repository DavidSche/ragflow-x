package handler

import (
	"encoding/json"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func parserPolicyContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func listParserPolicies(c *gin.Context) {
	tenantID, _ := parserPolicyContext(c)
	page, pageSize := pageParams(c)
	filter := repository.ParserPolicyFilter{
		ProjectID:    c.Query("project_id"),
		DatasetID:    c.Query("dataset_id"),
		DocumentType: c.Query("document_type"),
		ParseMode:    c.Query("parse_mode"),
	}
	if value, err := parseOptionalBool(c.Query("active")); err == nil {
		filter.Active = value
	} else {
		response.Fail(c, 400, 40081, "active must be true or false")
		return
	}
	items, total, err := handlerFrom(c).Service.ListParserPolicies(c.Request.Context(), tenantID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createParserPolicy(c *gin.Context) {
	tenantID, userID := parserPolicyContext(c)
	input, ok := bind[service.ParserPolicyInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateParserPolicy(c.Request.Context(), tenantID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	recordParserPolicyAudit(c, "parser_policy.create", "parser-policy", item.ID, item)
	_ = userID
	response.OK(c, item)
}

func getParserPolicy(c *gin.Context) {
	tenantID, _ := parserPolicyContext(c)
	item, err := handlerFrom(c).Service.GetParserPolicy(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	if item == nil {
		response.Err(c, httperr.NotFound("parser policy not found"))
		return
	}
	response.OK(c, item)
}

func updateParserPolicy(c *gin.Context) {
	tenantID, _ := parserPolicyContext(c)
	input, ok := bind[service.ParserPolicyPatchInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateParserPolicy(c.Request.Context(), tenantID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	recordParserPolicyAudit(c, "parser_policy.update", "parser-policy", item.ID, item)
	response.OK(c, item)
}

func deleteParserPolicy(c *gin.Context) {
	tenantID, userID := parserPolicyContext(c)
	id := c.Param("id")
	if err := handlerFrom(c).Service.DeleteParserPolicy(c.Request.Context(), tenantID, id); err != nil {
		response.Err(c, err)
		return
	}
	recordParserPolicyAudit(c, "parser_policy.delete", "parser-policy", id, nil)
	_ = userID
	response.OK(c, gin.H{"id": id})
}

func listQualityProfiles(c *gin.Context) {
	tenantID, _ := parserPolicyContext(c)
	page, pageSize := pageParams(c)
	var active *bool
	if value, err := parseOptionalBool(c.Query("active")); err == nil {
		active = value
	} else {
		response.Fail(c, 400, 40082, "active must be true or false")
		return
	}
	items, total, err := handlerFrom(c).Service.ListQualityProfiles(c.Request.Context(), tenantID, active, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createQualityProfile(c *gin.Context) {
	tenantID, userID := parserPolicyContext(c)
	input, ok := bind[service.QualityProfileInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateQualityProfile(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	recordParserPolicyAudit(c, "quality_profile.create", "quality-profile", item.ID, item)
	response.OK(c, item)
}

func getQualityProfile(c *gin.Context) {
	tenantID, _ := parserPolicyContext(c)
	item, err := handlerFrom(c).Service.GetQualityProfile(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	if item == nil {
		response.Err(c, httperr.NotFound("quality profile not found"))
		return
	}
	response.OK(c, item)
}

func updateQualityProfile(c *gin.Context) {
	tenantID, userID := parserPolicyContext(c)
	input, ok := bind[service.QualityProfilePatchInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateQualityProfile(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	recordParserPolicyAudit(c, "quality_profile.update", "quality-profile", item.ID, item)
	response.OK(c, item)
}

func deleteQualityProfile(c *gin.Context) {
	tenantID, userID := parserPolicyContext(c)
	id := c.Param("id")
	if err := handlerFrom(c).Service.DeleteQualityProfile(c.Request.Context(), tenantID, id); err != nil {
		response.Err(c, err)
		return
	}
	recordParserPolicyAudit(c, "quality_profile.delete", "quality-profile", id, nil)
	_ = userID
	response.OK(c, gin.H{"id": id})
}

func parseOptionalBool(value string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func recordParserPolicyAudit(c *gin.Context, action, resource, resourceID string, detail interface{}) {
	detailJSON := ""
	if detail != nil {
		if encoded, err := json.Marshal(detail); err == nil {
			detailJSON = string(encoded)
		}
	}
	h := handlerFrom(c)
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID:   c.GetString(middleware.ContextTenantID),
		UserID:     c.GetString(middleware.ContextUserID),
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		DetailJSON: detailJSON,
		IP:         c.ClientIP(),
		TraceID:    c.GetString("request_id"),
	})
}

func RegisterParserPolicyRoutes(group *gin.RouterGroup, h *Handler) {
	RegisterParseQualityRoutes(group)
	group.GET("/parser-policies", listParserPolicies)
	group.POST("/parser-policies", createParserPolicy)
	group.GET("/parser-policies/:id", getParserPolicy)
	group.PUT("/parser-policies/:id", updateParserPolicy)
	group.PATCH("/parser-policies/:id", updateParserPolicy)
	group.DELETE("/parser-policies/:id", deleteParserPolicy)
	group.GET("/quality-profiles", listQualityProfiles)
	group.POST("/quality-profiles", createQualityProfile)
	group.GET("/quality-profiles/:id", getQualityProfile)
	group.PUT("/quality-profiles/:id", updateQualityProfile)
	group.PATCH("/quality-profiles/:id", updateQualityProfile)
	group.DELETE("/quality-profiles/:id", deleteQualityProfile)
}
