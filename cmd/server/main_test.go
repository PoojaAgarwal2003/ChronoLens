package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/snapshot"
)

const record = `{"timestamp_us":1,"service":"a","duration_us":10,"status":200}`

func files(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(input, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<title>ChronoLens</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	return input, dir
}

func TestFlagsAndSetupFailures(t *testing.T) {
	tests := [][]string{
		{"-listen", "0.0.0.0:8080"}, {"-listen", "192.0.2.1:8080"},
		{"-listen", "127.0.0.1:invalid"}, {"-listen", "127.0.0.1:65536"},
		{"-input", ""}, {"-web", ""}, {"-max-events", "0"}, {"-max-events", "10000001"}, {"unexpected"},
		{"-format", "auto"}, {"-format", ""}, {"-format", "Snapshot"}, {"-format", " snapshot"},
	}
	for _, args := range tests {
		var out, errOut bytes.Buffer
		args = append([]string{"-input", filepath.Join(t.TempDir(), "missing")}, args...)
		if code := run(context.Background(), args, &out, &errOut); code != 2 || out.Len() != 0 || errOut.Len() == 0 {
			t.Fatalf("args %v returned %d: %s", args, code, &errOut)
		}
	}
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"-help"}, &out, &errOut); code != 0 {
		t.Fatal("help failed")
	}
	if !strings.Contains(errOut.String(), "snapshot") || !strings.Contains(errOut.String(), "-format") ||
		!strings.Contains(errOut.String(), `default "jsonl"`) {
		t.Fatalf("help omits input formats: %s", &errOut)
	}
	if code := run(context.Background(), []string{"-input", filepath.Join(t.TempDir(), "missing")}, &out, &errOut); code != 1 {
		t.Fatal("missing input succeeded")
	}
	input, _ := files(t)
	if code := run(context.Background(), []string{"-input", input, "-web", t.TempDir()}, &out, &errOut); code != 1 {
		t.Fatal("missing frontend build succeeded")
	}
}

type readyWriter struct{ ready chan string }

func (w readyWriter) Write(data []byte) (int, error) {
	w.ready <- strings.TrimSpace(strings.TrimPrefix(string(data), "ChronoLens listening on "))
	return len(data), nil
}

func TestServerLifecycle(t *testing.T) {
	for _, format := range []query.InputFormat{query.JSONLFormat, query.SnapshotFormat} {
		t.Run(string(format), func(t *testing.T) { testServerLifecycle(t, format) })
	}
}

func testServerLifecycle(t *testing.T, format query.InputFormat) {
	input, web := files(t)
	args := []string{"-input", input, "-web", web, "-listen", "127.0.0.1:0"}
	if format == query.SnapshotFormat {
		var encoded bytes.Buffer
		if _, err := snapshot.WriteJSONL(context.Background(), &encoded, strings.NewReader(record), 1); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(input, encoded.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-format", "snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	exit := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		exit <- run(ctx, args, readyWriter{ready}, &stderr)
	}()
	var url string
	select {
	case url = <-ready:
	case code := <-exit:
		t.Fatalf("server exited before becoming ready: %d: %s", code, &stderr)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not become ready")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(url + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != 200 || !bytes.Contains(body, []byte(`"status":"ok"`)) {
		t.Fatalf("unhealthy server: %s (%v / %v)", body, readErr, closeErr)
	}
	response, err = client.Get(url + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		InputFormat query.InputFormat `json:"input_format"`
		Rows        int               `json:"rows"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&meta)
	closeErr = response.Body.Close()
	if decodeErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || meta.InputFormat != format || meta.Rows != 1 {
		t.Fatalf("incorrect server metadata: %+v (%v / %v)", meta, decodeErr, closeErr)
	}
	for _, engine := range []string{"row", "columnar", "indexed"} {
		response, err = client.Post(url+"/api/query", "application/json",
			strings.NewReader(`{"engine":"`+engine+`","from_us":"0","buckets":1}`))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Result struct {
				Aggregate struct {
					Count uint64 `json:"count"`
					Sum   string `json:"duration_sum_us"`
				} `json:"aggregate"`
				Stats query.Stats `json:"stats"`
			} `json:"result"`
		}
		decodeErr = json.NewDecoder(response.Body).Decode(&result)
		closeErr = response.Body.Close()
		if decodeErr != nil || closeErr != nil || response.StatusCode != http.StatusOK ||
			result.Result.Aggregate.Count != 1 || result.Result.Aggregate.Sum != "10" || result.Result.Stats.Engine != query.Engine(engine) {
			t.Fatalf("incorrect %s query: %+v (%v / %v), status=%d", engine, result, decodeErr, closeErr, response.StatusCode)
		}
	}
	cancel()
	select {
	case code := <-exit:
		if code != 0 {
			t.Fatalf("graceful shutdown failed: %d: %s", code, &stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerFormatLoadFailures(t *testing.T) {
	var encoded bytes.Buffer
	if _, err := snapshot.WriteJSONL(context.Background(), &encoded, strings.NewReader(record+"\n"+record), 2); err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Clone(encoded.Bytes())
	corrupt[len(corrupt)-1] ^= 1
	for _, test := range []struct {
		name, input string
		args        []string
	}{
		{"default does not detect snapshot", encoded.String(), nil},
		{"snapshot rejects JSONL", record, []string{"-format", "snapshot"}},
		{"corrupt trailer", string(corrupt), []string{"-format", "snapshot"}},
		{"truncated trailer", encoded.String()[:encoded.Len()-1], []string{"-format", "snapshot"}},
		{"event limit", encoded.String(), []string{"-format", "snapshot", "-max-events", "1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, web := files(t)
			if err := os.WriteFile(input, []byte(test.input), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"-input", input, "-web", web, "-listen", "127.0.0.1:0"}, test.args...)
			if code := run(context.Background(), args, &stdout, &stderr); code != 1 || stdout.Len() != 0 ||
				!strings.Contains(stderr.String(), "load dataset") {
				t.Fatalf("invalid input started server: exit=%d stdout=%s stderr=%s", code, &stdout, &stderr)
			}
		})
	}
}
