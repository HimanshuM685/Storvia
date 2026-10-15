// Package metrics is a small Prometheus-compatible instrumentation registry.
//
// It exists because the alternative was a dependency, and because the counters this
// service needs are simple: a labelled counter, a gauge, and a histogram. The
// implementation is deliberately small and boring, because instrumentation that can
// itself fail is a liability in the code path it measures.
//
// The exposition format is Prometheus text format 0.0.4, so an existing scraper can read
// it with no configuration.
package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// contentType is the exposition format this registry emits.
const contentType = "text/plain; version=0.0.4; charset=utf-8"

// ContentType is the value an HTTP handler should set on the response.
func ContentType() string { return contentType }

// Registry holds every metric.
//
// Metric names are validated once at registration so a typo cannot create a series
// that silently accumulates under the wrong name forever.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*CounterVec
	gauges     map[string]*GaugeVec
	histograms map[string]*HistogramVec
	constants  map[string]*constant
	order      []string
}

type constant struct {
	name   string
	help   string
	labels map[string]string
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   map[string]*CounterVec{},
		gauges:     map[string]*GaugeVec{},
		histograms: map[string]*HistogramVec{},
		constants:  map[string]*constant{},
	}
}

// validateName rejects a metric name that a scraper would not accept.
//
// The rules are Prometheus's: letters, digits, underscores and colons, and it must not
// start with a digit.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("metrics: a metric needs a name")
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_', r == ':':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return fmt.Errorf("metrics: %q is not a valid metric name", name)
		}
	}
	return nil
}

// validateLabels checks a label set is usable.
func validateLabels(name string, labels map[string]string) error {
	for key := range labels {
		if key == "" {
			return fmt.Errorf("metrics: %q has an empty label name", name)
		}
	}
	return nil
}

func (r *Registry) track(name string) {
	if !contains(r.order, name) {
		r.order = append(r.order, name)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// labelKey renders a label set deterministically.
//
// Map iteration order is random, so the key is built from sorted labels. Without this
// the same label set would produce several distinct series depending on iteration
// order, and a counter would appear to reset at random.
func labelKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(labels[name]))
	}
	return b.String()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+`="`+escapeLabel(labels[name])+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return value
}

// Counter is a monotonically increasing value.
//
// It uses atomics rather than a mutex on the hot path: increments happen on every
// request, and a lock there is a contention point in code that is supposed to be
// free of side effects.
type Counter struct {
	value atomic.Uint64
}

// Add increases the counter.
func (c *Counter) Add(delta uint64) {
	if c == nil {
		return
	}
	c.value.Add(delta)
}

// Inc increases by one.
func (c *Counter) Inc() { c.Add(1) }

// Value reads the counter.
func (c *Counter) Value() uint64 {
	if c == nil {
		return 0
	}
	return c.value.Load()
}

// CounterVec is a counter with labels.
type CounterVec struct {
	name    string
	help    string
	mu      sync.RWMutex
	series  map[string]*Counter
	created map[string]map[string]string
}

// Counter returns the counter for a label set, creating it on first use.
func (v *CounterVec) Counter(labels map[string]string) *Counter {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := labelKey(labels)
	if c, ok := v.series[key]; ok {
		return c
	}
	c := &Counter{}
	v.series[key] = c
	v.created[key] = copyLabels(labels)
	return c
}

func copyLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

// Gauge is a value that can go up and down.
type Gauge struct {
	bits atomic.Uint64
}

// Set replaces the value.
func (g *Gauge) Set(v float64) {
	if g == nil {
		return
	}
	g.bits.Store(uint64(int64(v * 1e6)))
}

// Inc adds one.
func (g *Gauge) Inc() {
	if g == nil {
		return
	}
	for {
		old := g.bits.Load()
		cur := int64(old) + int64(1e6)
		if g.bits.CompareAndSwap(old, uint64(cur)) {
			return
		}
	}
}

// Dec subtracts one.
func (g *Gauge) Dec() {
	if g == nil {
		return
	}
	for {
		old := g.bits.Load()
		cur := int64(old) - int64(1e6)
		if g.bits.CompareAndSwap(old, uint64(cur)) {
			return
		}
	}
}

// Value reads the gauge.
func (g *Gauge) Value() float64 {
	if g == nil {
		return 0
	}
	return float64(int64(g.bits.Load())) / 1e6
}

// GaugeVec is a gauge with labels.
type GaugeVec struct {
	name    string
	help    string
	mu      sync.RWMutex
	series  map[string]*Gauge
	created map[string]map[string]string
}

// Gauge returns the gauge for a label set, creating it on first use.
func (v *GaugeVec) Gauge(labels map[string]string) *Gauge {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := labelKey(labels)
	if g, ok := v.series[key]; ok {
		return g
	}
	g := &Gauge{}
	v.series[key] = g
	v.created[key] = copyLabels(labels)
	return g
}

// Histogram records a distribution into fixed buckets.
//
// Buckets are declared in the caller's units and never renamed, because renaming one
// silently restarts every series under a new identity.
type Histogram struct {
	name    string
	help    string
	bounds  []float64
	mu      sync.Mutex
	counts  []uint64
	sum     float64
	total   uint64
	created map[string]string
}

// HistogramVec is a histogram with labels.
type HistogramVec struct {
	name   string
	help   string
	bounds []float64
	mu     sync.Mutex
	series map[string]*Histogram
}

// Histogram returns the histogram for a label set, creating it on first use.
func (v *HistogramVec) Histogram(labels map[string]string) *Histogram {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := labelKey(labels)
	if h, ok := v.series[key]; ok {
		return h
	}
	h := &Histogram{
		name:    v.name,
		help:    v.help,
		bounds:  v.bounds,
		counts:  make([]uint64, len(v.bounds)),
		created: copyLabels(labels),
	}
	v.series[key] = h
	return h
}

// Observe records a single measurement.
func (h *Histogram) Observe(value float64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	h.total++
	h.sum += value
	for i, bound := range h.bounds {
		if value <= bound {
			h.counts[i]++
		}
	}
}
