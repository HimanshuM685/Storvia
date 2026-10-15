package metrics

import (
	"strings"
	"sync"
	"testing"
)

func TestCounterAccumulates(t *testing.T) {
	counter := &Counter{}
	counter.Inc()
	counter.Add(41)
	if counter.Value() != 42 {
		t.Errorf("value = %d, want 42", counter.Value())
	}
}

func TestNilCounterIsSafe(t *testing.T) {
	// Instrumentation must never be the thing that crashes a request. A nil
	// collector means "this code path is not wired", and every call on it is a no-op.
	var counter *Counter
	counter.Inc()
	counter.Add(5)
	if counter.Value() != 0 {
		t.Error("a nil counter must read zero")
	}
	var gauge *Gauge
	gauge.Set(3)
	gauge.Inc()
	gauge.Dec()
	if gauge.Value() != 0 {
		t.Error("a nil gauge must read zero")
	}
	var hist *Histogram
	hist.Observe(1)
}

func TestGaugeGoesUpAndDown(t *testing.T) {
	gauge := &Gauge{}
	gauge.Set(5)
	gauge.Dec()
	if gauge.Value() != 4 {
		t.Errorf("value = %v, want 4", gauge.Value())
	}
	gauge.Inc()
	gauge.Inc()
	if gauge.Value() != 6 {
		t.Errorf("value = %v, want 6", gauge.Value())
	}
}

// TestLabelKeyIsOrderIndependent is the bug this guards. Go map iteration is
// randomised, so building a series key by ranging over the map would mint a new
// series on roughly half of all calls, and a counter would look like it resets.
func TestLabelKeyIsOrderIndependent(t *testing.T) {
	vec := NewRegistry().MustRegisterCounter("test_order", "help")

	for i := 0; i < 50; i++ {
		vec.Counter(map[string]string{"a": "1", "b": "2", "c": "3"}).Inc()
		vec.Counter(map[string]string{"c": "3", "b": "2", "a": "1"}).Inc()
	}
	if got := len(vec.series); got != 1 {
		t.Errorf("series = %d, want 1; the same label set produced several", got)
	}
	if got := vec.Counter(map[string]string{"a": "1", "b": "2", "c": "3"}).Value(); got != 100 {
		t.Errorf("count = %d, want 100", got)
	}
}

// TestExpositionFormatIsScrapable checks the output against what a Prometheus scraper
// requires: HELP, TYPE, and one sample per series.
func TestExpositionFormatIsScrapable(t *testing.T) {
	r := NewRegistry()
	counter := r.MustRegisterCounter("storvia_test_total", "A test counter.")
	gauge := r.MustRegisterGauge("storvia_test_gauge", "A test gauge.")
	hist := r.MustRegisterHistogram("storvia_test_seconds", "A test histogram.", []float64{0.1, 1})

	counter.Counter(map[string]string{"provider": "google_drive"}).Inc()
	gauge.Gauge(map[string]string{"tenant": "t"}).Set(1)
	hist.Histogram(map[string]string{"provider": "google_drive"}).Observe(0.5)

	out := r.Gather()

	for _, want := range []string{
		"# HELP storvia_test_total A test counter.",
		"# TYPE storvia_test_total counter",
		`storvia_test_total{provider="google_drive"} 1`,
		"# TYPE storvia_test_gauge gauge",
		`storvia_test_gauge{tenant="t"} 1`,
		// Labels are emitted in sorted order, which is the Prometheus convention and
		// is what makes two scrapes byte-comparable.
		`storvia_test_seconds_bucket{le="0.1",provider="google_drive"} 0`,
		`storvia_test_seconds_bucket{le="1",provider="google_drive"} 1`,
		`storvia_test_seconds_bucket{le="+Inf",provider="google_drive"} 1`,
		`storvia_test_seconds_count{provider="google_drive"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition is missing %q:\n%s", want, out)
		}
	}
}

// TestHistogramBucketsAreCumulative is a scraper correctness property: each bucket is
// the number of observations at or below its bound, not the number in that band.
func TestHistogramBucketsAreCumulative(t *testing.T) {
	r := NewRegistry()
	hist := r.MustRegisterHistogram("storvia_cumulative_seconds", "help", []float64{1, 5, 10})

	for _, value := range []float64{0.5, 2, 7, 20} {
		hist.Histogram(map[string]string{"k": "v"}).Observe(value)
	}

	out := r.Gather()
	for _, want := range []string{
		`le="1"} 1`,  // only 0.5
		`le="5"} 2`,  // 0.5 and 2
		`le="10"} 3`, // plus 7
		`le="+Inf"} 4`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestGatherIsDeterministic(t *testing.T) {
	r := NewRegistry()
	c := r.MustRegisterCounter("storvia_det_total", "help")
	for i := 0; i < 5; i++ {
		c.Counter(map[string]string{"z": "1", "a": "2", "m": "3"}).Inc()
	}
	first := r.Gather()
	for i := 0; i < 20; i++ {
		if again := r.Gather(); again != first {
			t.Fatal("Gather is not deterministic; a scrape that reorders makes diffs useless")
		}
	}
}

func TestInvalidMetricNamesAreRejected(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"", "1leading_digit", "has space", "has-dash"} {
		if _, err := r.RegisterCounter(name, "help"); err == nil {
			t.Errorf("%q was accepted as a metric name", name)
		}
	}
	if _, err := r.RegisterCounter("valid_name:colon", "help"); err != nil {
		t.Errorf("a valid name was rejected: %v", err)
	}
}

func TestRegisteringTwiceReturnsTheSameCollector(t *testing.T) {
	// Build can run more than once in a test, and a panic here would be baffling.
	r := NewRegistry()
	first := r.MustRegisterCounter("storvia_twice_total", "help")
	first.Counter(map[string]string{"a": "1"}).Inc()
	second := r.MustRegisterCounter("storvia_twice_total", "help")
	second.Counter(map[string]string{"a": "1"}).Inc()
	if got := second.Counter(map[string]string{"a": "1"}).Value(); got != 2 {
		t.Errorf("count = %d, want 2; re-registering replaced the live collector", got)
	}
}

// TestConcurrentUpdatesAreSafe. Metrics are written from every request goroutine, and
// a data race here is a crash under load in production.
func TestConcurrentUpdatesAreSafe(t *testing.T) {
	r := NewRegistry()
	counter := r.MustRegisterCounter("storvia_race_total", "help")
	gauge := r.MustRegisterGauge("storvia_race_gauge", "help")
	hist := r.MustRegisterHistogram("storvia_race_seconds", "help", []float64{1, 2})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			labels := map[string]string{"worker": "w"}
			for j := 0; j < 20; j++ {
				counter.Counter(labels).Inc()
				gauge.Gauge(labels).Inc()
				hist.Histogram(labels).Observe(float64(j % 3))
			}
		}(i)
	}
	wg.Wait()

	if got := counter.Counter(map[string]string{"worker": "w"}).Value(); got != 1000 {
		t.Errorf("count = %d, want 1000", got)
	}
	if got := gauge.Gauge(map[string]string{"worker": "w"}).Value(); got != 1000 {
		t.Errorf("gauge = %v, want 1000", got)
	}
	_ = r.Gather()
}

func TestLabelValuesAreEscaped(t *testing.T) {
	r := NewRegistry()
	c := r.MustRegisterCounter("storvia_escape_total", "help")
	c.Counter(map[string]string{"reason": `a"b\c`}).Inc()

	out := r.Gather()
	// An unescaped quote would end the label early and corrupt the whole scrape.
	if !strings.Contains(out, `{reason="a\"b\\c"}`) {
		t.Errorf("label not escaped:\n%s", out)
	}
}

func TestBuildInfoIsAlwaysOne(t *testing.T) {
	s := NewStorvia(map[string]string{"version": "dev", "go": "1.27"})
	out := s.Gather()
	if !strings.Contains(out, `storvia_build_info{go="1.27",version="dev"} 1`) {
		t.Errorf("build info missing:\n%s", out)
	}
}

func TestStorviaRecordHelpers(t *testing.T) {
	s := NewStorvia(map[string]string{"version": "dev"})

	s.RecordUploadStarted("google_drive")
	s.RecordUploadFinished("google_drive", "ready")
	s.RecordUploadFinished("s3_compatible", "unknown")
	s.RecordFallbackRouting("drive_unavailable_preflight")
	s.RecordBytesStored("s3_compatible", 2048)
	s.RecordProviderCall("google_drive", "Availability", "unavailable")
	s.ObserveUploadDuration("s3_compatible", 1.5)
	s.RecordJobRun("reconcile", "ok")
	s.RecordOrphans("s3_compatible", 3)
	s.SetConsent(true)
	s.SetObjects("s3_compatible", "ready", 4)

	out := s.Gather()
	for _, want := range []string{
		`storvia_uploads_started_total{provider="google_drive"} 1`,
		`storvia_uploads_finished_total{outcome="ready",provider="google_drive"} 1`,
		`storvia_uploads_outcome_unknown_total{provider="s3_compatible"} 1`,
		`storvia_fallback_routings_total{reason="drive_unavailable_preflight"} 1`,
		`storvia_bytes_stored_total{provider="s3_compatible"} 2048`,
		`storvia_provider_calls_total{class="unavailable",operation="Availability",provider="google_drive"} 1`,
		`storvia_job_runs_total{kind="reconcile",outcome="ok"} 1`,
		`storvia_orphans_found_total{provider="s3_compatible"} 3`,
		`storvia_fallback_consent{tenant="current"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// TestNilStorviaIsSafe is the property that keeps instrumentation out of the blast
// radius. A deployment that has not wired metrics must lose observation, not requests.
func TestNilStorviaIsSafe(t *testing.T) {
	var s *Storvia
	s.RecordUploadStarted("google_drive")
	s.RecordUploadFinished("google_drive", "ready")
	s.RecordFallbackRouting("reason")
	s.RecordBytesStored("google_drive", 10)
	s.RecordProviderCall("google_drive", "Availability", "unavailable")
	s.ObserveUploadDuration("google_drive", 1)
	s.SetConsent(true)
	s.SetObjects("google_drive", "ready", 1)
	s.RecordJobRun("reconcile", "ok")
	s.RecordOrphans("google_drive", 1)
	if got := s.Gather(); got != "" {
		t.Errorf("Gather on nil returned %q, want empty", got)
	}
}
