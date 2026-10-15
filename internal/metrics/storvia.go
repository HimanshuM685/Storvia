package metrics

import (
	"sync"
)

// Storvia is the application's metric set.
//
// The collectors are chosen to answer the questions this service actually has to
// answer in production, which are all about a routing decision:
//
//   - which provider did an upload land on, and did it finish
//   - how often the fallback opened, and why
//   - how often an outcome became unknown, because that is the case where a person has
//     to go and look
//   - how large the uploads are, so a timeout can be read as the client's pace rather
//     than as evidence about Drive's health
//
// Counters that are obviously zero everywhere would be noise, so there are none.
type Storvia struct {
	Registry *Registry

	UploadsStarted  *CounterVec
	UploadsFinished *CounterVec
	UploadsUnknown  *CounterVec
	BytesStored     *CounterVec
	FallbackRouting *CounterVec
	ProviderCalls   *CounterVec
	ConsentState    *GaugeVec
	UploadDuration  *HistogramVec
	JobRuns         *CounterVec
	OrphansFound    *CounterVec
	ObjectsStored   *GaugeVec

	once sync.Once
}

// StorviaBucket bounds upload duration.
//
// The top bucket is an hour because the server allows a 30-minute upload; anything
// beyond that is not an upload any more.
var StorviaBucket = []float64{
	0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// NewStorvia builds the application's metric set.
func NewStorvia(buildLabels map[string]string) *Storvia {
	r := NewRegistry()
	s := &Storvia{Registry: r}

	s.UploadsStarted = r.MustRegisterCounter("storvia_uploads_started_total",
		"Uploads planned, by the provider they were routed to.")
	s.UploadsFinished = r.MustRegisterCounter("storvia_uploads_finished_total",
		"Uploads that reached a terminal state, by provider and outcome.")
	s.UploadsUnknown = r.MustRegisterCounter("storvia_uploads_outcome_unknown_total",
		"Operations whose result could not be established. Each one is work for a person.")
	s.BytesStored = r.MustRegisterCounter("storvia_bytes_stored_total",
		"Bytes committed to a provider location, by provider.")
	s.FallbackRouting = r.MustRegisterCounter("storvia_fallback_routings_total",
		"Uploads routed to the fallback provider, by the reason Drive was not used.")
	s.ProviderCalls = r.MustRegisterCounter("storvia_provider_calls_total",
		"Provider operations, by provider, operation and failure class.")
	s.JobRuns = r.MustRegisterCounter("storvia_job_runs_total",
		"Background job runs, by kind and outcome.")
	s.OrphansFound = r.MustRegisterCounter("storvia_orphans_found_total",
		"Objects present at a provider with no Storvia record, by provider.")

	s.ConsentState = r.MustRegisterGauge("storvia_fallback_consent",
		"Whether this tenant may route to the fallback: 1 granted, 0 not granted.")
	s.ObjectsStored = r.MustRegisterGauge("storvia_objects_stored",
		"Objects held for this tenant, by provider and state.")

	s.UploadDuration = r.MustRegisterHistogram("storvia_upload_duration_seconds",
		"Time from planning an upload to committing it.", StorviaBucket)

	r.SetBuildInfo("storvia_build_info",
		"Build and runtime information; always 1.", buildLabels)

	s.once.Do(func() {})
	return s
}

// Every method below tolerates a nil receiver.
//
// Instrumentation that can panic takes down the request it is measuring, and a
// deployment that has not wired metrics should lose observation rather than
// correctness. A nil *Storvia is the "not wired" case.

// RecordUploadStarted notes a new upload plan.
func (s *Storvia) RecordUploadStarted(provider string) {
	if s == nil {
		return
	}
	s.UploadsStarted.Counter(map[string]string{"provider": provider}).Inc()
}

// RecordUploadFinished notes a terminal upload outcome.
//
// outcome is one of ready, failed, cancelled, or unknown.
func (s *Storvia) RecordUploadFinished(provider, outcome string) {
	if s == nil {
		return
	}
	s.UploadsFinished.Counter(map[string]string{"provider": provider, "outcome": outcome}).Inc()
	if outcome == "unknown" {
		s.UploadsUnknown.Counter(map[string]string{"provider": provider}).Inc()
	}
}

// RecordFallbackRouting notes that Drive was skipped.
func (s *Storvia) RecordFallbackRouting(reason string) {
	if s == nil {
		return
	}
	if reason == "" {
		reason = "unspecified"
	}
	s.FallbackRouting.Counter(map[string]string{"reason": reason}).Inc()
}

// RecordBytesStored notes committed bytes.
func (s *Storvia) RecordBytesStored(provider string, bytes int64) {
	if s == nil {
		return
	}
	if bytes <= 0 {
		return
	}
	s.BytesStored.Counter(map[string]string{"provider": provider}).Add(uint64(bytes))
}

// RecordProviderCall notes one provider operation and how it was classified.
//
// The class label is the point: it makes "how often is Drive unavailable" and "how
// often is Drive refusing us" answerable without reading logs.
func (s *Storvia) RecordProviderCall(provider, operation, class string) {
	if s == nil {
		return
	}
	if class == "" {
		class = "none"
	}
	s.ProviderCalls.Counter(map[string]string{
		"provider": provider, "operation": operation, "class": class,
	}).Inc()
}

// ObserveUploadDuration records how long an upload took end to end.
func (s *Storvia) ObserveUploadDuration(provider string, seconds float64) {
	if s == nil {
		return
	}
	s.UploadDuration.Histogram(map[string]string{"provider": provider}).Observe(seconds)
}

// SetConsent records whether the fallback is open for this tenant.
func (s *Storvia) SetConsent(granted bool) {
	if s == nil {
		return
	}
	value := 0.0
	if granted {
		value = 1
	}
	s.ConsentState.Gauge(map[string]string{"tenant": "current"}).Set(value)
}

// SetObjects records the tenant's object counts.
func (s *Storvia) SetObjects(provider, state string, count int) {
	if s == nil {
		return
	}
	s.ObjectsStored.Gauge(map[string]string{"provider": provider, "state": state}).Set(float64(count))
}

// RecordJobRun notes a background job outcome.
func (s *Storvia) RecordJobRun(kind, outcome string) {
	if s == nil {
		return
	}
	s.JobRuns.Counter(map[string]string{"kind": kind, "outcome": outcome}).Inc()
}

// RecordOrphans notes objects at a provider with no Storvia record.
func (s *Storvia) RecordOrphans(provider string, count int) {
	if s == nil {
		return
	}
	if count <= 0 {
		return
	}
	s.OrphansFound.Counter(map[string]string{"provider": provider}).Add(uint64(count))
}

// Gather renders the exposition output.
func (s *Storvia) Gather() string {
	if s == nil || s.Registry == nil {
		return ""
	}
	return s.Registry.Gather()
}
