package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

const testData = `{"timestamp_us":1,"service":"a","duration_us":10,"status":200}
{"timestamp_us":2,"service":"b","duration_us":20,"status":500}
{"timestamp_us":2,"service":"a","duration_us":0,"status":503}
{"timestamp_us":3,"service":"a","duration_us":40,"status":404}
`
const validRequest = `{"engine":"indexed","from_us":"2","to_us":"3","service":"","status":0,"buckets":100}`

func testServer(t *testing.T) *Server {
	t.Helper()
	catalog, err := query.LoadCatalog(context.Background(), strings.NewReader(testData), 10)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(catalog, fstest.MapFS{
		"index.html":   {Data: []byte("<!doctype html><title>ChronoLens</title>")},
		"assets/ui.js": {Data: []byte("console.log('local')")},
	}, "fixture.jsonl", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func call(server *Server, method, route, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+route, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	return w
}

func TestMetadataHealthAndAssets(t *testing.T) {
	server := testServer(t)
	w := call(server, http.MethodGet, "/api/meta", "")
	var meta metadataResponse
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil || w.Code != 200 ||
		meta.Rows != 4 || meta.ServiceCount != 2 || *meta.MinUS != "1" || *meta.MaxUS != "3" ||
		len(meta.Engines) != 3 || meta.MaxBuckets != 240 || meta.Dataset != "fixture.jsonl" {
		t.Fatalf("invalid metadata: %s, %v", w.Body, err)
	}
	for _, route := range []string{"/", "/assets/ui.js", "/api/health"} {
		response := call(server, http.MethodGet, route, "")
		if response.Code != 200 || response.Header().Get("X-Content-Type-Options") != "nosniff" ||
			!strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("invalid route %s: %+v", route, response)
		}
	}
	if w := call(server, http.MethodGet, "/assets/", ""); w.Code != 404 {
		t.Fatal("static directory listing must not be exposed")
	}
}

func TestQueryWireContractAndExactProfiles(t *testing.T) {
	server := testServer(t)
	for _, engine := range []string{"row", "columnar", "indexed"} {
		w := call(server, http.MethodPost, "/api/query", strings.Replace(validRequest, "indexed", engine, 1))
		var result queryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
			t.Fatalf("query failed: %s, %v", w.Body, err)
		}
		if result.Result.Aggregate.Count != 2 || result.Result.Aggregate.ErrorCount != 2 ||
			result.Result.Aggregate.DurationSumUS != "20" || len(result.Profile.Timeline) != 1 ||
			result.Profile.Timeline[0].Count != 2 || result.Profile.RowsExamined != 2 {
			t.Fatalf("query/profile disagreement: %+v", result)
		}
		want := 4
		if engine == "indexed" {
			want = 2
		}
		if result.Result.Stats.RowsExamined != want || result.Result.Stats.Engine != query.Engine(engine) {
			t.Fatalf("wrong engine stats: %+v", result.Result.Stats)
		}
		if len(server.slots) != 0 {
			t.Fatal("query leaked admission slot")
		}
	}
}

func TestRequestValidation(t *testing.T) {
	server := testServer(t)
	tests := []struct {
		name, body string
		status     int
	}{
		{"invalid JSON", "{", 400},
		{"array", "[]", 400},
		{"null", "null", 400},
		{"unknown field", strings.Replace(validRequest, `"buckets":100`, `"unexpected":1,"buckets":100`, 1), 400},
		{"second object", validRequest + validRequest, 400},
		{"wrong engine", strings.Replace(validRequest, "indexed", "bad", 1), 400},
		{"numeric timestamp loses wire contract", strings.Replace(validRequest, `"from_us":"2"`, `"from_us":2`, 1), 400},
		{"timestamp overflow", strings.Replace(validRequest, `"from_us":"2"`, `"from_us":"9223372036854775808"`, 1), 400},
		{"negative timestamp", strings.Replace(validRequest, `"from_us":"2"`, `"from_us":"-1"`, 1), 400},
		{"reversed range", strings.Replace(validRequest, `"to_us":"3"`, `"to_us":"1"`, 1), 400},
		{"invalid upper bound", strings.Replace(validRequest, `"to_us":"3"`, `"to_us":"bad"`, 1), 400},
		{"oversized status", strings.Replace(validRequest, `"status":0`, `"status":65536`, 1), 400},
		{"invalid status", strings.Replace(validRequest, `"status":0`, `"status":600`, 1), 400},
		{"bucket cap", strings.Replace(validRequest, `"buckets":100`, `"buckets":241`, 1), 400},
		{"zero buckets", strings.Replace(validRequest, `"buckets":100`, `"buckets":0`, 1), 400},
		{"oversized body", validRequest + strings.Repeat(" ", maxBodyBytes), 413},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := call(server, http.MethodPost, "/api/query", test.body)
			if w.Code != test.status || !strings.Contains(w.Body.String(), `"error":`) || len(server.slots) != 0 {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
		})
	}
	for _, route := range []string{"/api/query", "/api/compare"} {
		if w := call(server, http.MethodGet, route, ""); w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatal("API method restriction failed")
		}
	}
	if w := call(server, http.MethodGet, "/api/absent", ""); w.Code != 404 || !strings.Contains(w.Body.String(), `"error":`) {
		t.Fatal("unknown API route must return JSON 404")
	}
}

func TestLocalOnlyAndAdmissionGuards(t *testing.T) {
	server := testServer(t)
	for _, mutation := range []func(*http.Request){
		func(r *http.Request) { r.Host = "attacker.example:8080" },
		func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
		func(r *http.Request) { r.Header.Set("Origin", "null") },
	} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/query", strings.NewReader(validRequest))
		r.Header.Set("Content-Type", "application/json")
		mutation(r)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("untrusted origin/host accepted: %d", w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/query", strings.NewReader(validRequest))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("simple cross-origin-compatible content type accepted")
	}
	server.slots <- struct{}{}
	server.slots <- struct{}{}
	w = call(server, http.MethodPost, "/api/query", validRequest)
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("saturated server did not reject work")
	}
	if w := call(server, http.MethodGet, "/api/health", ""); w.Code != 200 {
		t.Fatal("health should remain available while queries are saturated")
	}
	<-server.slots
	<-server.slots
}

func TestCancellationAndComparison(t *testing.T) {
	server := testServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/query", strings.NewReader(validRequest)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 408 || len(server.slots) != 0 {
		t.Fatalf("cancelled request leaked results or capacity: %d", w.Code)
	}
	w = call(server, http.MethodPost, "/api/compare", validRequest)
	var response struct {
		Results    []comparison `json:"results"`
		Equivalent bool         `json:"equivalent"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 ||
		!response.Equivalent || len(response.Results) != 3 {
		t.Fatalf("comparison failed: %s (%v)", w.Body, err)
	}
	for _, value := range response.Results {
		if value.Iterations < 1 || value.Iterations > maxIterations || value.Aggregate.Count != 2 {
			t.Fatalf("invalid comparison work/result: %+v", value)
		}
		if value.StableWindow {
			if value.MeanQueryMS == nil || *value.MeanQueryMS <= 0 {
				t.Fatal("resolved comparison must have positive mean timing")
			}
		} else if value.MeanQueryMS != nil {
			t.Fatal("insufficient measurement window must not claim a mean")
		}
	}
}

func TestMissingAssetsAndEmptyCatalog(t *testing.T) {
	catalog, err := query.LoadCatalog(context.Background(), strings.NewReader(""), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(catalog, fstest.MapFS{}, "empty", 0); err == nil {
		t.Fatal("missing frontend build must fail explicitly")
	}
	if _, err := New(nil, fstest.MapFS{}, "empty", 0); err == nil {
		t.Fatal("missing engine catalog accepted")
	}
	server, err := New(catalog, fstest.MapFS{"index.html": {Data: []byte("empty")}}, "empty", 0)
	if err != nil {
		t.Fatal(err)
	}
	w := call(server, http.MethodGet, "/api/meta", "")
	var meta metadataResponse
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil || meta.MinUS != nil || meta.MaxUS != nil || meta.Rows != 0 {
		t.Fatal("empty catalog metadata failed")
	}
	w = call(server, http.MethodPost, "/api/query", validRequest)
	if w.Code != 200 {
		t.Fatalf("empty dataset query failed: %s", w.Body)
	}
}

func TestHTTPPreservesLargeIntegerBoundaries(t *testing.T) {
	input := `{"timestamp_us":9223372036854775807,"service":"a","duration_us":4294967295,"status":500}`
	catalog, err := query.LoadCatalog(context.Background(), strings.NewReader(input), 10)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(catalog, fstest.MapFS{"index.html": {Data: []byte("test")}}, "large", 0)
	if err != nil {
		t.Fatal(err)
	}
	w := call(server, http.MethodPost, "/api/query",
		`{"engine":"indexed","from_us":"9223372036854775807","to_us":null,"service":"","status":0,"buckets":100}`)
	var response queryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 ||
		response.Result.Aggregate.DurationSumUS != "4294967295" ||
		len(response.Profile.Timeline) != 1 || response.Profile.Timeline[0].FromUS != "9223372036854775807" ||
		response.Profile.Timeline[0].ToUS != "9223372036854775808" {
		t.Fatalf("HTTP integer precision lost: %s (%v)", w.Body, err)
	}
}

func TestMetadataPayloadLimitAndDeadline(t *testing.T) {
	var input strings.Builder
	for i := range 260 {
		fmt.Fprintf(&input, "{\"timestamp_us\":%d,\"service\":\"s%03d\",\"duration_us\":1,\"status\":200}\n", i, i)
	}
	catalog, err := query.LoadCatalog(context.Background(), strings.NewReader(input.String()), 260)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(catalog, fstest.MapFS{"index.html": {Data: []byte("test")}}, "many-services", 0)
	if err != nil {
		t.Fatal(err)
	}
	w := call(server, http.MethodGet, "/api/meta", "")
	var meta metadataResponse
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil || meta.ServiceCount != 260 ||
		len(meta.Services) != 256 || !meta.ServicesTruncated {
		t.Fatalf("metadata bound incorrect: %s (%v)", w.Body, err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/query", strings.NewReader(validRequest)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 504 || len(server.slots) != 0 || strings.Contains(w.Body.String(), `"result"`) {
		t.Fatalf("deadline returned partial output or leaked admission capacity: %d %s", w.Code, w.Body)
	}
}
