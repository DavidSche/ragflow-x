package obs

import (
	"fmt"

	dto "github.com/prometheus/client_model/go"
)

// MetricValue returns the current value of a registered metric child selected
// by label values (in declaration order of the metric's label names). It is a
// governance-introspection helper used by contract tests (137 §11.1) and by
// operators correlating alert expressions with raw samples. It returns 0 when
// the metric or the requested child does not exist yet.
func (o *Observability) MetricValue(name string, labelValues ...string) float64 {
	if o == nil || o.registry == nil {
		return 0
	}
	families, err := o.registry.Gather()
	if err != nil {
		return 0
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricMatchesLabels(metric, labelValues) {
				return metricValue(metric)
			}
		}
	}
	return 0
}

func metricMatchesLabels(metric *dto.Metric, want []string) bool {
	labels := metric.GetLabel()
	if len(labels) != len(want) {
		return false
	}
	// Prometheus gathers label pairs sorted by label name, so match
	// order-independently by value multiset.
	got := make(map[string]int, len(labels))
	for _, label := range labels {
		got[label.GetValue()]++
	}
	for _, value := range want {
		if got[value] == 0 {
			return false
		}
		got[value]--
	}
	return true
}

func metricValue(metric *dto.Metric) float64 {
	switch {
	case metric.GetCounter() != nil:
		return metric.GetCounter().GetValue()
	case metric.GetGauge() != nil:
		return metric.GetGauge().GetValue()
	case metric.GetHistogram() != nil:
		return float64(metric.GetHistogram().GetSampleCount())
	case metric.GetSummary() != nil:
		return float64(metric.GetSummary().GetSampleCount())
	default:
		return 0
	}
}

// MetricFamilies lists gathered metric names for diagnostics.
func (o *Observability) MetricFamilies() []string {
	if o == nil || o.registry == nil {
		return nil
	}
	families, err := o.registry.Gather()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, fmt.Sprintf("%s:%d", family.GetName(), len(family.GetMetric())))
	}
	return names
}
