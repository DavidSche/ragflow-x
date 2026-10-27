package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"strconv"
)

// UsageReportRow is a per (tenant,date) usage and estimated-cost line.
type UsageReportRow struct {
	TenantID      string  `json:"tenant_id"`
	TenantName    string  `json:"tenant_name"`
	Date          string  `json:"date"`
	TokensIn      int64   `json:"tokens_in"`
	TokensOut     int64   `json:"tokens_out"`
	Requests      int64   `json:"requests"`
	EstimatedCost float64 `json:"estimated_cost"`
	Currency      string  `json:"currency"`
}

// UsageReport aggregates metered usage into per (tenant,date) rows and derives
// estimated cost from the configured per-1K-token rate.
func (s *Service) UsageReport(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string) ([]UsageReportRow, error) {
	rows, err := s.Store.SummarizeUsageRange(ctx, tenantID, scopeAll, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	name := map[string]string{}
	if scopeAll {
		if tenants, err := s.Store.ListAllTenants(ctx); err == nil {
			for _, tenant := range tenants {
				name[tenant.ID] = tenant.Name
			}
		}
	}
	rate := s.EstimatedCostPer1K / 1000
	out := make([]UsageReportRow, 0, len(rows))
	for _, row := range rows {
		estimatedCost := (float64(row.TokensIn) + float64(row.TokensOut)) * rate
		out = append(out, UsageReportRow{
			TenantID:      row.TenantID,
			TenantName:    name[row.TenantID],
			Date:          row.Date,
			TokensIn:      row.TokensIn,
			TokensOut:     row.TokensOut,
			Requests:      row.Requests,
			EstimatedCost: estimatedCost,
			Currency:      s.EstimatedCostCurrency,
		})
	}
	return out, nil
}

// ExportUsageCSV renders usage rows as a CSV byte stream.
func (s *Service) ExportUsageCSV(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string) ([]byte, error) {
	rows, err := s.UsageReport(ctx, tenantID, scopeAll, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	_ = writer.Write([]string{"tenant_id", "tenant", "date", "tokens_in", "tokens_out", "requests", "estimated_cost", "currency"})
	for _, row := range rows {
		_ = writer.Write([]string{
			row.TenantID, row.TenantName, row.Date,
			strconv.FormatInt(row.TokensIn, 10), strconv.FormatInt(row.TokensOut, 10),
			strconv.FormatInt(row.Requests, 10), strconv.FormatFloat(row.EstimatedCost, 'f', 4, 64), row.Currency,
		})
	}
	writer.Flush()
	return buf.Bytes(), writer.Error()
}
