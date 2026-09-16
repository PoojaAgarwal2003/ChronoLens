package load

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func config(url string) Config {
	return Config{Target: url + "/api/query", Payload: json.RawMessage(`{}`), Duration: time.Millisecond, Rate: 1, MaxInflight: 1, MaxRequests: 1, Timeout: time.Second}
}

func TestValidationAndBounds(t *testing.T) {
	for _, url := range []string{
		"http://localhost:80/api/query", "http://192.0.2.1:80/api/query", "https://127.0.0.1:80/api/query",
		"http://127.0.0.1/api/query", "http://127.0.0.1:0/api/query", "http://127.0.0.1:80/api/meta",
		"http://u:p@127.0.0.1:80/api/query", "http://127.0.0.1:80/api/query?x=1",
		"http://127.0.0.1:80/api/query?", "http://127.0.0.1:80/api/query#x", "http://127.0.0.1:80/api/%71uery",
		"http://127.0.0.1:80/api/query#",
	} {
		c := config("")
		c.Target = url
		if c.Validate() == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	for _, url := range []string{"http://127.0.0.1:80/api/query", "http://[::1]:80/api/compare"} {
		c := config("")
		c.Target = url
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Duration = 0 }, func(c *Config) { c.Duration = time.Minute + 1 },
		func(c *Config) { c.Rate = 0 }, func(c *Config) { c.Rate = 1001 },
		func(c *Config) { c.MaxInflight = 0 }, func(c *Config) { c.MaxInflight = 65 },
		func(c *Config) { c.MaxRequests = 0 }, func(c *Config) { c.MaxRequests = MaxSamples + 1 },
		func(c *Config) { c.Timeout = 0 }, func(c *Config) { c.Timeout = 10*time.Second + 1 },
		func(c *Config) { c.Payload = json.RawMessage(`null`) },
		func(c *Config) { c.Payload = json.RawMessage(`{}` + strings.Repeat(" ", 4096)) },
	} {
		c := config("http://127.0.0.1:80")
		mutate(&c)
		if c.Validate() == nil {
			t.Fatal("accepted invalid bounds")
		}
	}
}

func TestScheduleAndAccounting(t *testing.T) {
	c := config("http://127.0.0.1:80")
	c.Duration, c.Rate, c.MaxRequests = time.Second, 3, MaxSamples
	s := schedule(c)
	if len(s) != 3 || s[0].ScheduledMS != 0 || s[1].ScheduledMS != ms(time.Second/3) {
		t.Fatalf("%+v", s)
	}
	c.Duration = time.Second + 1
	if len(schedule(c)) != 4 {
		t.Fatal("fractional final arrival lost")
	}
	c.Duration, c.Rate = time.Minute, 1000
	if len(schedule(c)) != MaxSamples {
		t.Fatal("sample cap not applied")
	}
	for _, tt := range []struct {
		lag  time.Duration
		full bool
		want string
	}{
		{0, false, ""}, {time.Second - 1, false, ""}, {time.Second, false, "skipped_late"},
		{0, true, "skipped_inflight"}, {2 * time.Second, true, "skipped_late"},
	} {
		if got := skipReason(tt.lag, time.Second, tt.full); got != tt.want {
			t.Fatalf("%s != %s", got, tt.want)
		}
	}
	r := Report{Samples: []Sample{
		{Outcome: "success", Status: 200, StartedMS: new(0.0), LatencyMS: new(100.0)},
		{Outcome: "rejected", Status: 429, StartedMS: new(1.0), LatencyMS: new(1.0)},
		{Outcome: "client_timeout", StartedMS: new(2.0), LatencyMS: new(200.0)},
		{Outcome: "transport_error", StartedMS: new(3.0), LatencyMS: new(2.0)},
		{Outcome: "skipped_inflight"}, {Outcome: "skipped_late"}, {Outcome: "skipped_cancelled"},
	}}
	summarize(&r)
	if r.Sent != 4 || r.Skipped != 3 || r.Scheduled != r.Sent+r.Skipped || r.Success.Count != 1 || *r.Success.P95 != 100 || r.Statuses[429] != 1 || r.Lag.Count != 6 {
		t.Fatalf("%+v", r)
	}
}

func TestNearestRank(t *testing.T) {
	if Percentiles(nil).P50 != nil {
		t.Fatal("empty quantile must be null")
	}
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(100 - i)
	}
	q := Percentiles(values)
	if *q.P50 != 50 || *q.P95 != 95 || *q.P99 != 99 || values[0] != 100 {
		t.Fatalf("%+v", q)
	}
	q = Percentiles([]float64{3, 1, 2})
	if *q.P50 != 2 || *q.P95 != 3 {
		t.Fatal(q)
	}
}

func TestHTTPOutcomesAndNoRedirectOrProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://192.0.2.1:9999")
	t.Setenv("HTTPS_PROXY", "http://192.0.2.1:9999")
	for _, tt := range []struct {
		status        int
		body, outcome string
	}{
		{200, `{"ok":true}`, "success"}, {429, `{"error":"busy"}`, "rejected"},
		{504, `{"error":"deadline"}`, "server_timeout"}, {408, `{}`, "server_timeout"},
		{500, `{"error":"broken"}`, "http_error"}, {302, `redirect`, "http_error"},
		{200, `bad`, "invalid_response"}, {200, strings.Repeat("x", maxResponse+1), "transport_error"},
	} {
		t.Run(tt.outcome+string(rune(tt.status)), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/meta" {
					io.WriteString(w, `{"rows":1000000}`)
					return
				}
				calls.Add(1)
				w.Header().Set("Location", "http://192.0.2.1:9999/escape")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer server.Close()
			r, err := Run(context.Background(), config(server.URL))
			if err != nil || calls.Load() != 1 || r.Sent != 1 || r.Outcomes[tt.outcome] != 1 || r.Samples[0].Status != tt.status {
				t.Fatalf("%+v %v calls=%d", r, err, calls.Load())
			}
			if tt.outcome != "success" && r.Samples[0].Error == "" {
				t.Fatal("lost error")
			}
			if tt.status == 429 && r.Samples[0].RetryAfter != "1" {
				t.Fatal("lost retry header")
			}
		})
	}
}

func TestInflightCancellationRecovery(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/meta" {
			io.WriteString(w, `{}`)
			return
		}
		close(entered)
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		close(exited)
	}))
	defer server.Close()
	c := config(server.URL)
	c.Duration, c.Rate, c.MaxRequests = 500*time.Millisecond, 100, 50
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Report, 1)
	go func() { r, _ := Run(ctx, c); done <- r }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("not entered")
	}
	cancel()
	select {
	case r := <-done:
		if r.Sent != 1 || r.Outcomes["cancelled"] != 1 || r.RunError == "" || r.Scheduled != r.Sent+r.Skipped {
			t.Fatalf("%+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not drain")
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not observe disconnect")
	}
}

func TestInflightCapAndClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/meta" {
			io.WriteString(w, `{}`)
			return
		}
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	c := config(server.URL)
	c.Duration, c.Rate, c.MaxRequests, c.Timeout = 50*time.Millisecond, 100, 5, 200*time.Millisecond
	r, err := Run(context.Background(), c)
	if err != nil || r.Sent != 1 || r.Outcomes["client_timeout"] != 1 || r.Skipped != 4 {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Success.Count != 0 {
		t.Fatal("timeout counted as success")
	}
}

func TestTransportFailureAndPreCancellation(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	r, err := Run(context.Background(), config(url))
	if err != nil || r.MetadataError == "" || r.Outcomes["transport_error"] != 1 || r.Samples[0].Error == "" {
		t.Fatalf("%+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err = Run(ctx, config(url))
	if !errors.Is(err, context.Canceled) || r.Sent != 0 || r.Outcomes["skipped_cancelled"] != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}
