package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// RegisterCounter declares a labelled counter.
//
// It is idempotent so that wiring called twice (a test, or a re-Build) does not panic
// or silently replace a live collector.
func (r *Registry) RegisterCounter(name, help string) (*CounterVec, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.counters[name]; ok {
		return existing, nil
	}
	vec := &CounterVec{name: name, help: help, series: map[string]*Counter{}, created: map[string]map[string]string{}}
	r.counters[name] = vec
	r.track(name)
	return vec, nil
}

// RegisterGauge declares a labelled gauge.
func (r *Registry) RegisterGauge(name, help string) (*GaugeVec, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.gauges[name]; ok {
		return existing, nil
	}
	vec := &GaugeVec{name: name, help: help, series: map[string]*Gauge{}, created: map[string]map[string]string{}}
	r.gauges[name] = vec
	r.track(name)
	return vec, nil
}

// RegisterHistogram declares a labelled histogram over the given bucket bounds.
func (r *Registry) RegisterHistogram(name, help string, bounds []float64) (*HistogramVec, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	sorted := append([]float64(nil), bounds...)
	sort.Float64s(sorted)

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.histograms[name]; ok {
		return existing, nil
	}
	vec := &HistogramVec{name: name, help: help, bounds: sorted, series: map[string]*Histogram{}}
	r.histograms[name] = vec
	r.track(name)
	return vec, nil
}

// MustRegister declares a metric and panics on an invalid definition.
//
// A bad metric name is a programming error, not a runtime condition, and it should
// fail loudly at start rather than produce an endpoint that silently omits a series.
func (r *Registry) MustRegisterCounter(name, help string) *CounterVec {
	vec, err := r.RegisterCounter(name, help)
	if err != nil {
		panic(err)
	}
	return vec
}

func (r *Registry) MustRegisterGauge(name, help string) *GaugeVec {
	vec, err := r.RegisterGauge(name, help)
	if err != nil {
		panic(err)
	}
	return vec
}

func (r *Registry) MustRegisterHistogram(name, help string, bounds []float64) *HistogramVec {
	vec, err := r.RegisterHistogram(name, help, bounds)
	if err != nil {
		panic(err)
	}
	return vec
}

// SetBuildInfo records a constant informational metric.
//
// It is how an operator answers "which version is this?" without correlating against a
// deployment timestamp.
func (r *Registry) SetBuildInfo(name, help string, labels map[string]string) {
	if err := validateName(name); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.constants[name]; !ok {
		r.order = append(r.order, name)
	}
	r.constants[name] = &constant{name: name, help: help, labels: copyLabels(labels)}
}

// Gather renders the registry in Prometheus text exposition format.
//
// Output is deterministic: metric families come in registration order and label sets in
// sorted order. A scrape that reorders between calls makes diffs useless.
func (r *Registry) Gather() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var b strings.Builder

	for _, name := range r.order {
		if c, ok := r.constants[name]; ok {
			writeHeader(&b, c.name, c.help, c.name)
			fmt.Fprintf(&b, "%s%s 1\n", c.name, formatLabels(c.labels))
			b.WriteByte('\n')
			continue
		}

		if vec, ok := r.counters[name]; ok {
			writeHeader(&b, vec.name, vec.help, "counter")
			for _, key := range sortedKeys(vec.created) {
				fmt.Fprintf(&b, "%s%s %d\n", vec.name, formatLabels(vec.created[key]), vec.series[key].Value())
			}
			b.WriteByte('\n')
			continue
		}

		if vec, ok := r.gauges[name]; ok {
			writeHeader(&b, vec.name, vec.help, "gauge")
			for _, key := range sortedKeys(vec.created) {
				fmt.Fprintf(&b, "%s%s %s\n", vec.name, formatLabels(vec.created[key]),
					formatFloat(vec.series[key].Value()))
			}
			b.WriteByte('\n')
			continue
		}

		if vec, ok := r.histograms[name]; ok {
			writeHeader(&b, vec.name, vec.help, "histogram")
			for _, key := range sortedKeysOfHistograms(vec) {
				h := vec.series[key]
				labels := h.created
				for i, bound := range h.bounds {
					fmt.Fprintf(&b, "%s_bucket%s %d\n", vec.name,
						mergeLabels(labels, map[string]string{"le": formatFloat(bound)}), h.counts[i])
				}
				fmt.Fprintf(&b, "%s_bucket%s %d\n", vec.name,
					mergeLabels(labels, map[string]string{"le": "+Inf"}), h.total)
				fmt.Fprintf(&b, "%s_sum%s %s\n", vec.name, formatLabels(labels), formatFloat(h.sum))
				fmt.Fprintf(&b, "%s_count%s %d\n", vec.name, formatLabels(labels), h.total)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func writeHeader(b *strings.Builder, name, help, typ string) {
	if help != "" {
		fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	}
	if typ != "" && typ != "counter" {
		fmt.Fprintf(b, "# TYPE %s %s\n", name, typ)
	} else {
		fmt.Fprintf(b, "# TYPE %s counter\n", name)
	}
}

func sortedKeys(created map[string]map[string]string) []string {
	keys := make([]string, 0, len(created))
	for key := range created {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysOfHistograms(vec *HistogramVec) []string {
	keys := make([]string, 0, len(vec.series))
	for key := range vec.series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeLabels(base, extra map[string]string) string {
	merged := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return formatLabels(merged)
}

func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
