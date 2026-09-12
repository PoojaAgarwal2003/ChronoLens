package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	}
	for _, args := range tests {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), args, &out, &errOut); code != 2 {
			t.Fatalf("args %v returned %d: %s", args, code, &errOut)
		}
	}
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"-help"}, &out, &errOut); code != 0 {
		t.Fatal("help failed")
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
	input, web := files(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	exit := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		exit <- run(ctx, []string{"-input", input, "-web", web, "-listen", "127.0.0.1:0"}, readyWriter{ready}, &stderr)
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
