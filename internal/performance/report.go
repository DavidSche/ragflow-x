package performance

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RenderMarkdown produces the human-readable baseline report. Durations are
// emitted with whole milliseconds to avoid implying false precision.
func RenderMarkdown(report *Report) ([]byte, error) {
	if report == nil {
		return nil, fmt.Errorf("performance: nil report")
	}
	var out strings.Builder
	out.WriteString("# 性能基线报告\n\n")
	out.WriteString("## 运行环境\n\n")
	out.WriteString("| 项目 | 值 |\n|---|---|\n")
	env := report.Environment
	rows := [][2]string{
		{"生成时间", report.GeneratedAt.Format(time.RFC3339)},
		{"版本", report.Version},
		{"主机", env.Host},
		{"操作系统", env.OS},
		{"Go 版本", env.GoVersion},
		{"GOMAXPROCS", fmt.Sprintf("%d", env.GOMAXPROCS)},
		{"CPU", env.CPUModel},
		{"内存", env.MemoryGB},
		{"数据库", env.Database},
		{"Upstream", env.Upstream},
		{"数据规模", env.DataScale},
		{"知识库数量", fmt.Sprintf("%d", env.KnowledgeBases)},
		{"Git Revision", env.GitRevision},
	}
	for _, row := range rows {
		out.WriteString("| " + row[0] + " | " + escapeMarkdown(row[1]) + " |\n")
	}

	out.WriteString("\n## 采样策略\n\n")
	out.WriteString("| 项目 | 值 |\n|---|---|\n")
	out.WriteString("| 单并发采样窗口 | " + report.Sampling.DurationPerConcurrency.String() + " |\n")
	out.WriteString("| 单并发预热窗口 | " + report.Sampling.WarmupPerConcurrency.String() + " |\n")
	out.WriteString("| 并发档位 | " + ints(report.Sampling.ConcurrencyLevels) + " |\n")
	out.WriteString("| 单次请求超时 | " + report.Sampling.TimeoutPerAttempt.String() + " |\n")

	out.WriteString("\n## 资源峰值\n\n")
	out.WriteString("| 项目 | 值 |\n|---|---|\n")
	out.WriteString("| Heap Alloc 峰值 | " + formatBytes(report.Resources.HeapAllocBytes) + " |\n")
	out.WriteString("| Heap Sys 峰值 | " + formatBytes(report.Resources.HeapSysBytes) + " |\n")
	out.WriteString("| Goroutine 峰值 | " + fmt.Sprintf("%d", report.Resources.Goroutines) + " |\n")
	out.WriteString("| 资源采样次数 | " + fmt.Sprintf("%d", report.Resources.SampledAt) + " |\n")

	for _, scenario := range report.Scenarios {
		out.WriteString("\n## " + scenario.Name + "\n\n")
		if scenario.Description != "" {
			out.WriteString(scenario.Description + "\n\n")
		}
		out.WriteString("| 并发 | QPS | P50(ms) | P95(ms) | P99(ms) | Max(ms) | 错误率 |")
		if hasTTFT(scenario) {
			out.WriteString(" TTFT P50/P95/P99(ms) |")
		}
		out.WriteString("\n|---:|---:|---:|---:|---:|---:|---:|")
		if hasTTFT(scenario) {
			out.WriteString("---:|")
		}
		out.WriteString("\n")
		for _, result := range scenario.Results {
			out.WriteString(fmt.Sprintf(
				"| %d | %.1f | %.3f | %.3f | %.3f | %.3f | %.2f%% |",
				result.Concurrency, result.Throughput,
				ms(result.P50), ms(result.P95), ms(result.P99), ms(result.Max), result.ErrorRate,
			))
			if hasTTFT(scenario) {
				out.WriteString(fmt.Sprintf(" %.3f / %.3f / %.3f |",
					ms(pointer(result.TTFTP50)), ms(pointer(result.TTFTP95)), ms(pointer(result.TTFTP99))))
			}
			out.WriteString("\n")
		}
		if len(report.Summary) > 0 && report.Summary[scenario.Name] != "" {
			out.WriteString("\n结论：" + report.Summary[scenario.Name] + "\n")
		}
	}
	out.WriteString("\n## 结果解读约束\n\n")
	out.WriteString("- 本报告只适用于相同硬件、版本、数据规模和 upstream 类型之间的回归比较。\n")
	out.WriteString("- `mock/controlled upstream` 不代表生产模型推理延迟；生产 RAGFlow/模型版本必须单独复测。\n")
	out.WriteString("- 错误率包含状态码、协议校验与超时；任何非零错误率都需要在发布前归因。\n")
	return []byte(out.String()), nil
}

// WriteJSON emits canonical machine-readable output.
func WriteJSON(report *Report) ([]byte, error) {
	if report == nil {
		return nil, fmt.Errorf("performance: nil report")
	}
	return json.MarshalIndent(report, "", "  ")
}

func hasTTFT(scenario ScenarioResult) bool {
	for _, result := range scenario.Results {
		if result.TTFTP50 != nil {
			return true
		}
	}
	return false
}

func pointer(value *time.Duration) time.Duration {
	if value == nil {
		return 0
	}
	return *value
}

func ms(value time.Duration) float64 { return float64(value) / float64(time.Millisecond) }

func ints(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = fmt.Sprintf("%d", value)
	}
	return strings.Join(parts, ", ")
}

func escapeMarkdown(value string) string { return strings.ReplaceAll(value, "|", "\\|") }

func formatBytes(value uint64) string {
	const kb = 1024
	switch {
	case value >= kb*kb*kb:
		return fmt.Sprintf("%.2f GiB", float64(value)/(kb*kb*kb))
	case value >= kb*kb:
		return fmt.Sprintf("%.2f MiB", float64(value)/(kb*kb))
	case value >= kb:
		return fmt.Sprintf("%.2f KiB", float64(value)/kb)
	default:
		return fmt.Sprintf("%d B", value)
	}
}
