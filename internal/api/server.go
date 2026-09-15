// Package api serves the local explorer. It deliberately does not expose file
// uploads, arbitrary input paths, or a public unauthenticated deployment mode.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/measure"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

const (
	maxBodyBytes        = 4096
	maxMetadataServices = 256
	queryTimeout        = 3 * time.Second
	measurementWindow   = 50 * time.Millisecond
	maxIterations       = 100000
)

type Server struct {
	catalog *query.Catalog
	handler http.Handler
	slots   chan struct{}
}

type metadataResponse struct {
	Dataset           string            `json:"dataset"`
	InputFormat       query.InputFormat `json:"input_format"`
	Rows              int               `json:"rows"`
	ServiceCount      int               `json:"service_count"`
	Services          []string          `json:"services"`
	ServicesTruncated bool              `json:"services_truncated"`
	Statuses          []uint16          `json:"statuses"`
	MinUS             *string           `json:"min_timestamp_us"`
	MaxUS             *string           `json:"max_timestamp_us"`
	Engines           []query.Engine    `json:"engines"`
	DefaultBuckets    int               `json:"default_buckets"`
	MaxBuckets        int               `json:"max_buckets"`
	LoadMS            *float64          `json:"load_ms"`
}

type request struct {
	Engine  string  `json:"engine"`
	FromUS  string  `json:"from_us"`
	ToUS    *string `json:"to_us"`
	Service string  `json:"service"`
	Status  uint16  `json:"status"`
	Buckets int     `json:"buckets"`
}

type wireAggregate struct {
	Count          uint64   `json:"count"`
	ErrorCount     uint64   `json:"error_count"`
	DurationSumUS  string   `json:"duration_sum_us"`
	MeanDurationUS *float64 `json:"mean_duration_us"`
	MinDurationUS  *uint32  `json:"min_duration_us"`
	MaxDurationUS  *uint32  `json:"max_duration_us"`
}

func aggregate(value query.Aggregate) wireAggregate {
	return wireAggregate{
		Count: value.Count, ErrorCount: value.ErrorCount, DurationSumUS: strconv.FormatUint(value.DurationSumUS, 10),
		MeanDurationUS: value.MeanDurationUS, MinDurationUS: value.MinDurationUS, MaxDurationUS: value.MaxDurationUS,
	}
}

type wireResult struct {
	Aggregate wireAggregate `json:"aggregate"`
	Stats     query.Stats   `json:"stats"`
}

type timings struct {
	QueryMS      *float64 `json:"query_ms"`
	ProfileMS    *float64 `json:"profile_ms"`
	ServerWorkMS *float64 `json:"server_work_ms"`
}

type queryResponse struct {
	Result  wireResult    `json:"result"`
	Profile query.Profile `json:"profile"`
	Timing  timings       `json:"timing"`
}

type comparison struct {
	Engine        query.Engine  `json:"engine"`
	Aggregate     wireAggregate `json:"aggregate"`
	Stats         query.Stats   `json:"stats"`
	Iterations    int           `json:"iterations"`
	MeanQueryMS   *float64      `json:"mean_query_ms"`
	MeasurementMS *float64      `json:"measurement_ms"`
	StableWindow  bool          `json:"stable_window"`
}

func New(catalog *query.Catalog, assets fs.FS, dataset string, loadTime time.Duration) (*Server, error) {
	if _, err := catalog.Engine(query.Indexed); err != nil {
		return nil, err
	}
	info, err := fs.Stat(assets, "index.html")
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("frontend build is missing: run npm ci and npm run build in web")
	}
	meta := catalog.Metadata()
	response := metadataResponse{
		Dataset: dataset, InputFormat: catalog.InputFormat(), Rows: meta.Rows, ServiceCount: len(meta.Services),
		Services: meta.Services, Statuses: meta.Statuses,
		Engines:        []query.Engine{query.Row, query.Columnar, query.Indexed},
		DefaultBuckets: 100, MaxBuckets: query.MaxBuckets, LoadMS: measure.Milliseconds(loadTime),
	}
	if len(response.Services) > maxMetadataServices {
		response.Services = response.Services[:maxMetadataServices]
		response.ServicesTruncated = true
	}
	if meta.MinUS != nil {
		first, last := strconv.FormatInt(*meta.MinUS, 10), strconv.FormatInt(*meta.MaxUS, 10)
		response.MinUS, response.MaxUS = &first, &last
	}
	server := &Server{catalog: catalog, slots: make(chan struct{}, 2)}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if method(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}
	})
	mux.HandleFunc("/api/meta", func(w http.ResponseWriter, r *http.Request) {
		if method(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, response)
		}
	})
	mux.HandleFunc("/api/query", server.execute)
	mux.HandleFunc("/api/compare", server.compare)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Unknown API route")
	})
	files := http.FileServer(http.FS(assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET and HEAD are supported")
			return
		}
		if r.URL.Path != "/" {
			info, err := fs.Stat(assets, strings.TrimPrefix(path.Clean(r.URL.Path), "/"))
			if err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
	server.handler = mux
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		writeError(w, http.StatusForbidden, "invalid_host", "Only a loopback Host is accepted")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		writeError(w, http.StatusForbidden, "cross_origin", "Cross-origin requests are not allowed")
		return
	}
	s.handler.ServeHTTP(w, r)
}

func method(w http.ResponseWriter, r *http.Request, expected string) bool {
	if r.Method == expected {
		return true
	}
	w.Header().Set("Allow", expected)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Unsupported HTTP method")
	return false
}

func (s *Server) prepare(w http.ResponseWriter, r *http.Request) (query.Engine, query.Filter, int, bool) {
	if !method(w, r, http.MethodPost) {
		return "", query.Filter{}, 0, false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "content_type", "Content-Type must be application/json")
		return "", query.Filter{}, 0, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input request
	err = decoder.Decode(&input)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); trailing != io.EOF {
			if trailing != nil {
				err = trailing
			} else {
				err = errors.New("expected a single JSON object")
			}
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Request body exceeds 4096 bytes")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", "Request must be one JSON object with recognized fields and types")
		}
		return "", query.Filter{}, 0, false
	}
	engine, err := query.ParseEngine(input.Engine)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_engine", err.Error())
		return "", query.Filter{}, 0, false
	}
	from, err := strconv.ParseInt(input.FromUS, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_range", "from_us must be a decimal int64 string")
		return "", query.Filter{}, 0, false
	}
	filter := query.Filter{FromUS: from, Service: input.Service, Status: input.Status}
	if input.ToUS != nil {
		to, err := strconv.ParseInt(*input.ToUS, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_range", "to_us must be a decimal int64 string or null")
			return "", query.Filter{}, 0, false
		}
		filter.ToUS = &to
	}
	if err := filter.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return "", query.Filter{}, 0, false
	}
	if input.Buckets < 1 || input.Buckets > query.MaxBuckets {
		writeError(w, http.StatusBadRequest, "invalid_buckets", "buckets must be between 1 and 240")
		return "", query.Filter{}, 0, false
	}
	select {
	case s.slots <- struct{}{}:
		return engine, filter, input.Buckets, true
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "busy", "Two queries are already active; retry shortly")
		return "", query.Filter{}, 0, false
	}
}

func (s *Server) execute(w http.ResponseWriter, r *http.Request) {
	engine, filter, buckets, ok := s.prepare(w, r)
	if !ok {
		return
	}
	defer func() { <-s.slots }()
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	start := time.Now()
	data, err := s.catalog.Engine(engine)
	if err != nil {
		executionError(w, err)
		return
	}
	queryStart := time.Now()
	result, err := data.Query(ctx, filter)
	queryTime := time.Since(queryStart)
	if err != nil {
		executionError(w, err)
		return
	}
	indexed, err := s.catalog.Engine(query.Indexed)
	if err != nil {
		executionError(w, err)
		return
	}
	profileStart := time.Now()
	profile, err := indexed.Profile(ctx, filter, buckets)
	profileTime := time.Since(profileStart)
	if err != nil {
		executionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, queryResponse{
		Result:  wireResult{Aggregate: aggregate(result.Aggregate), Stats: result.Stats},
		Profile: profile,
		Timing:  timings{QueryMS: measure.Milliseconds(queryTime), ProfileMS: measure.Milliseconds(profileTime), ServerWorkMS: measure.Milliseconds(time.Since(start))},
	})
}

func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	_, filter, _, ok := s.prepare(w, r)
	if !ok {
		return
	}
	defer func() { <-s.slots }()
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	results := make([]comparison, 0, 3)
	for _, engine := range []query.Engine{query.Row, query.Columnar, query.Indexed} {
		data, err := s.catalog.Engine(engine)
		if err != nil {
			executionError(w, err)
			return
		}
		value, err := compareEngine(ctx, data, filter)
		if err != nil {
			executionError(w, err)
			return
		}
		if len(results) != 0 && !reflect.DeepEqual(results[0].Aggregate, value.Aggregate) {
			writeError(w, http.StatusInternalServerError, "inconsistent_results", "Query engines produced different aggregates")
			return
		}
		results = append(results, value)
	}
	writeJSON(w, http.StatusOK, struct {
		Results    []comparison `json:"results"`
		Equivalent bool         `json:"equivalent"`
	}{results, true})
}

func compareEngine(ctx context.Context, data *query.Dataset, filter query.Filter) (comparison, error) {
	start := time.Now()
	var result query.Result
	var elapsed time.Duration
	iterations := 0
	for iterations < maxIterations {
		var err error
		result, err = data.Query(ctx, filter)
		if err != nil {
			return comparison{}, err
		}
		iterations++
		elapsed = time.Since(start)
		if iterations >= 3 && elapsed >= measurementWindow {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return comparison{}, err
	}
	value := comparison{
		Engine: result.Stats.Engine, Aggregate: aggregate(result.Aggregate), Stats: result.Stats,
		Iterations: iterations, MeasurementMS: measure.Milliseconds(elapsed), StableWindow: elapsed >= measurementWindow,
	}
	if value.StableWindow {
		mean := float64(elapsed) / float64(time.Millisecond) / float64(iterations)
		value.MeanQueryMS = &mean
	}
	return value, nil
}

func executionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "Query exceeded its three-second budget")
	case errors.Is(err, context.Canceled):
		writeError(w, http.StatusRequestTimeout, "cancelled", "Query was cancelled")
	default:
		log.Printf("query execution failed: %v", err)
		writeError(w, http.StatusInternalServerError, "execution_failed", "Query execution failed")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("encode response: %v", err)
		http.Error(w, "Response encoding failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(data, '\n')); err != nil {
		log.Printf("write response: %v", err)
	}
}
