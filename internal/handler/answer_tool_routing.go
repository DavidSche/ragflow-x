package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func executeRoutedSQLToolAnswer(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	input, ok := bind[service.RoutedSQLToolAnswerInput](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.ExecuteRoutedSQLToolAnswer(
		c.Request.Context(), tenantID, userID, input.AssistantID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func executeRoutedSQLMultiSubqueryAnswer(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	input, ok := bind[service.RoutedSQLMultiSubqueryAnswerInput](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.ExecuteRoutedSQLMultiSubqueryAnswer(
		c.Request.Context(), tenantID, userID, input.AssistantID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func executeRoutedMixedToolAnswer(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	input, ok := bind[service.RoutedMixedToolAnswerInput](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.ExecuteRoutedMixedToolAnswer(
		c.Request.Context(), tenantID, userID, input.AssistantID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func executePlannedRoutedMixedToolAnswer(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	input, ok := bind[service.RoutedPlannedToolAnswerInput](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.PlanRoutedMixedToolAnswer(
		c.Request.Context(), tenantID, userID, input.AssistantID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}
