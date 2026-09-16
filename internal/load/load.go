// Package load runs bounded, open-loop experiments against numeric loopback HTTP.
package load

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const MaxSamples = 10000
const maxResponse = 1 << 20

type Config struct {
	Target      string          `json:"target"`
	Payload     json.RawMessage `json:"payload"`
	Duration    time.Duration   `json:"duration_ns"`
	Rate        int             `json:"arrivals_per_second"`
	MaxInflight int             `json:"max_inflight"`
	MaxRequests int             `json:"max_requests"`
	Timeout     time.Duration   `json:"request_timeout_ns"`
}

func (c Config) Validate() error {
	if _, err := target(c.Target); err != nil {
		return err
	}
	if c.Duration < time.Millisecond || c.Duration > time.Minute || c.Rate < 1 || c.Rate > 1000 ||
		c.MaxInflight < 1 || c.MaxInflight > 64 || c.MaxRequests < 1 || c.MaxRequests > MaxSamples ||
		c.Timeout < time.Millisecond || c.Timeout > 10*time.Second {
		return errors.New("duration must be 1ms..1m, rate 1..1000, inflight 1..64, requests 1..10000, timeout 1ms..10s")
	}
	if len(c.Payload) > 4096 || !json.Valid(c.Payload) || len(bytes.TrimSpace(c.Payload)) == 0 || bytes.TrimSpace(c.Payload)[0] != '{' {
		return errors.New("payload must be a JSON object of at most 4096 bytes")
	}
	return nil
}

func target(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	ip := net.ParseIP(u.Hostname())
	port, portErr := strconv.Atoi(u.Port())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || portErr != nil || port < 1 || port > 65535 ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.RawPath != "" ||
		(u.Path != "/api/query" && u.Path != "/api/compare") {
		return nil, errors.New("target must be http://numeric-loopback:port/api/query or /api/compare without credentials, query, or fragment")
	}
	return u, nil
}

type Sample struct {
	Index         int      `json:"index"`
	ScheduledMS   float64  `json:"scheduled_ms"`
	StartedMS     *float64 `json:"started_ms,omitempty"`
	LagMS         float64  `json:"scheduler_lag_ms"`
	LatencyMS     *float64 `json:"complete_latency_ms,omitempty"`
	Outcome       string   `json:"outcome"`
	Status        int      `json:"http_status,omitempty"`
	RetryAfter    string   `json:"retry_after,omitempty"`
	Error         string   `json:"error,omitempty"`
	ResponseBytes int      `json:"response_bytes,omitempty"`
}

type Quantiles struct {
	Count int      `json:"count"`
	P50   *float64 `json:"p50_ms"`
	P95   *float64 `json:"p95_ms"`
	P99   *float64 `json:"p99_ms"`
}

// Percentiles uses nearest rank on individual samples, never batch averages.
func Percentiles(values []float64) Quantiles {
	values = append([]float64(nil), values...)
	sort.Float64s(values)
	q := Quantiles{Count: len(values)}
	if len(values) == 0 {
		return q
	}
	at := func(p float64) *float64 { v := values[int(math.Ceil(p*float64(len(values))))-1]; return &v }
	q.P50, q.P95, q.P99 = at(.5), at(.95), at(.99)
	return q
}

type Report struct {
	Schema        int             `json:"schema"`
	Config        Config          `json:"config"`
	StartedUTC    string          `json:"started_utc"`
	ElapsedMS     float64         `json:"elapsed_ms"`
	Policy        string          `json:"policy"`
	Environment   map[string]any  `json:"environment"`
	Metadata      json.RawMessage `json:"dataset_metadata,omitempty"`
	MetadataError string          `json:"metadata_error,omitempty"`
	RunError      string          `json:"run_error,omitempty"`
	Scheduled     int             `json:"scheduled"`
	Sent          int             `json:"sent"`
	Skipped       int             `json:"skipped"`
	Outcomes      map[string]int  `json:"outcomes"`
	Statuses      map[int]int     `json:"http_statuses"`
	Success       Quantiles       `json:"successful_complete_requests"`
	Lag           Quantiles       `json:"observed_scheduler_lag"`
	Samples       []Sample        `json:"samples"`
}

func schedule(c Config) []Sample {
	n := min(c.MaxRequests, int((int64(c.Duration)*int64(c.Rate)+int64(time.Second)-1)/int64(time.Second)))
	result := make([]Sample, n)
	for i := range result {
		result[i] = Sample{Index: i, ScheduledMS: ms(time.Duration(int64(i) * int64(time.Second) / int64(c.Rate)))}
	}
	return result
}

func skipReason(lag, period time.Duration, full bool) string {
	if lag >= period {
		return "skipped_late"
	}
	if full {
		return "skipped_inflight"
	}
	return ""
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// Run has no workload retries or catch-up bursts. Every planned arrival has a
// sample, including local skips. Elapsed includes draining accepted requests.
func Run(ctx context.Context, c Config) (Report, error) {
	if err := c.Validate(); err != nil {
		return Report{}, err
	}
	transport := &http.Transport{
		Proxy: nil, MaxConnsPerHost: c.MaxInflight, MaxIdleConns: c.MaxInflight,
		MaxIdleConnsPerHost: c.MaxInflight, MaxResponseHeaderBytes: 16 << 10,
		DisableCompression: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			ip := net.ParseIP(host)
			if err != nil || ip == nil || !ip.IsLoopback() {
				return nil, errors.New("refusing nonnumeric/nonloopback dial")
			}
			return (&net.Dialer{Timeout: c.Timeout}).DialContext(ctx, network, address)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r := Report{Schema: 1, Config: c, Policy: "Open-loop fixed arrivals; no workload retries; skip arrivals >= one period late or at inflight cap; drain on normal completion; cancel on parent cancellation. Metadata probe excluded from schedule and elapsed.",
		Environment: map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "client_server": "same-host loopback"},
		Samples:     schedule(c)}
	u, _ := target(c.Target)
	u.Path = "/api/meta"
	metaReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	meta, err := client.Do(metaReq)
	if err != nil {
		r.MetadataError = err.Error()
	} else {
		body, readErr := readBody(meta.Body)
		if readErr != nil {
			r.MetadataError = readErr.Error()
		} else if meta.StatusCode != 200 || !json.Valid(body) {
			r.MetadataError = fmt.Sprintf("metadata HTTP %d: %s", meta.StatusCode, body)
		} else {
			r.Metadata = body
		}
	}
	start := time.Now()
	r.StartedUTC = start.UTC().Format(time.RFC3339Nano)
	slots := make(chan struct{}, c.MaxInflight)
	var wg sync.WaitGroup
	period := time.Second / time.Duration(c.Rate)
	for i := range r.Samples {
		s := &r.Samples[i]
		due := start.Add(time.Duration(int64(i) * int64(time.Second) / int64(c.Rate)))
		wait := time.Until(due)
		if wait > 0 && ctx.Err() == nil {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
			}
			timer.Stop()
		}
		if ctx.Err() != nil {
			s.Outcome = "skipped_cancelled"
			continue
		}
		lag := time.Since(due)
		s.LagMS = ms(lag)
		s.Outcome = skipReason(lag, period, len(slots) == cap(slots))
		if s.Outcome != "" {
			continue
		}
		slots <- struct{}{}
		wg.Add(1)
		go func(s *Sample, due time.Time) {
			defer wg.Done()
			defer func() { <-slots }()
			begin := time.Now()
			// Goroutine dispatch can itself be late; do not send a catch-up burst.
			s.LagMS = ms(begin.Sub(due))
			if begin.Sub(due) >= period {
				s.Outcome = "skipped_late"
				return
			}
			s.StartedMS = new(ms(begin.Sub(start)))
			execute(ctx, client, c, s)
			s.LatencyMS = new(ms(time.Since(begin)))
		}(s, due)
	}
	wg.Wait()
	r.ElapsedMS = ms(time.Since(start))
	if err := ctx.Err(); err != nil {
		r.RunError = err.Error()
	}
	summarize(&r)
	return r, ctx.Err()
}

func readBody(body io.ReadCloser) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponse+1))
	err = errors.Join(err, body.Close())
	if len(data) > maxResponse {
		err = errors.Join(err, errors.New("response exceeds 1 MiB"))
	}
	return data, err
}

func execute(ctx context.Context, client *http.Client, c Config, s *Sample) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Target, bytes.NewReader(c.Payload))
	if err != nil {
		s.Outcome, s.Error = "transport_error", err.Error()
		return
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err == nil {
		s.Status, s.RetryAfter = response.StatusCode, response.Header.Get("Retry-After")
		var body []byte
		body, err = readBody(response.Body)
		s.ResponseBytes = len(body)
		if err == nil {
			switch {
			case s.Status == 200:
				if json.Valid(body) {
					s.Outcome = "success"
				} else {
					s.Outcome, s.Error = "invalid_response", "HTTP 200 body is not valid JSON"
				}
			case s.Status == 429:
				s.Outcome = "rejected"
			case s.Status == 408 || s.Status == 504:
				s.Outcome = "server_timeout"
			default:
				s.Outcome = "http_error"
			}
			if s.Status != 200 {
				// Error payloads are retained with an explicit truncation marker.
				if len(body) > 4096 {
					s.Error = string(body[:4096]) + " [truncated at 4096 bytes]"
				} else {
					s.Error = string(body)
				}
			}
			return
		}
	}
	s.Outcome, s.Error = "transport_error", err.Error()
	var ne net.Error
	if errors.Is(err, context.Canceled) {
		s.Outcome = "cancelled"
	} else if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
		s.Outcome = "client_timeout"
	}
}

func summarize(r *Report) {
	r.Scheduled = len(r.Samples)
	r.Outcomes, r.Statuses = make(map[string]int), make(map[int]int)
	var success, lag []float64
	for _, s := range r.Samples {
		r.Outcomes[s.Outcome]++
		if s.StartedMS != nil {
			r.Sent++
		} else {
			r.Skipped++
		}
		if s.Status != 0 {
			r.Statuses[s.Status]++
		}
		if s.Outcome == "success" && s.LatencyMS != nil {
			success = append(success, *s.LatencyMS)
		}
		if s.Outcome != "skipped_cancelled" {
			lag = append(lag, s.LagMS)
		}
	}
	r.Success, r.Lag = Percentiles(success), Percentiles(lag)
}
