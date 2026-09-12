package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

func TestRunStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-events", "3", "-output", "-"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	if bytes.Count(stdout.Bytes(), []byte("\n")) != 3 {
		t.Fatalf("expected three JSONL records, got %q", &stdout)
	}
	decoder := json.NewDecoder(&stdout)
	for i := 0; i < 3; i++ {
		var event telemetry.Event
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(stderr.String(), "Generated 3 events") {
		t.Fatalf("missing completion message: %s", &stderr)
	}
}

func TestRunFileAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "events.jsonl")
	var stdout, stderr bytes.Buffer
	args := []string{"-events", "2", "-output", path}
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(before, []byte("\n")) != 2 || stdout.Len() != 0 {
		t.Fatal("file output did not contain exactly two events or polluted stdout")
	}
	stderr.Reset()
	if code := run(context.Background(), args, &stdout, &stderr); code != 1 {
		t.Fatalf("expected file-exists failure, got %d: %s", code, &stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("existing file changed")
	}
}

func TestRunInvalidArguments(t *testing.T) {
	tests := [][]string{
		{"-events", "0"}, {"-events", "invalid"}, {"-services", "1025"},
		{"-start", "not-a-time"}, {"-start", "2026-01-01T00:00:00.0000001Z"},
		{"-start", "2026-01-01T00:00:00.0000000001Z"},
		{"-interval", "1ns"}, {"-error-percent", "101"}, {"-unknown"}, {"unexpected"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "should-not-exist.jsonl")
			var stdout, stderr bytes.Buffer
			fullArgs := append([]string{"-output", path}, args...)
			if code := run(context.Background(), fullArgs, &stdout, &stderr); code != 2 {
				t.Fatalf("expected usage error, got %d: %s", code, &stderr)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid arguments created output: %v", err)
			}
			if stderr.Len() == 0 || stdout.Len() != 0 {
				t.Fatal("expected error on stderr and empty stdout")
			}
		})
	}
}

func TestRunStartWithOffsetAndExactFraction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"-events", "1", "-start", "2026-01-01T01:00:00.123456000+01:00", "-output", "-"}
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	var event telemetry.Event
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.TimestampUS != 1767225600123456 {
		t.Fatalf("timezone or fractional precision lost: %d", event.TimestampUS)
	}
}

func TestRunEmptyOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-output", ""}, &stdout, &stderr); code != 2 {
		t.Fatalf("expected usage error, got %d", code)
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help exited with %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage: generator") || stdout.Len() != 0 {
		t.Fatal("missing usage text or unexpected stdout")
	}
}

func TestRunCancelledCreatesNoFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "cancelled.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run(ctx, []string{"-events", "5", "-output", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("expected cancellation failure, got %d", code)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled command created output: %v", err)
	}
}

// Cancel on a deterministic context check, after output-file creation.
type cancelAfterChecks struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelAfterChecks) Err() error {
	c.checks++
	if c.checks == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestRunCancellationRemovesPartialFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlled := &cancelAfterChecks{Context: ctx, cancel: cancel}
	path := filepath.Join(t.TempDir(), "partial.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run(controlled, []string{"-events", "5", "-output", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("expected cancellation failure, got %d: %s", code, &stderr)
	}
	if controlled.checks != 3 {
		t.Fatalf("expected cancellation during generation, got %d checks", controlled.checks)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial output was not cleaned up: %v", err)
	}
	if strings.Contains(stderr.String(), "Generated") {
		t.Fatal("reported success after cancellation")
	}
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) {
	return 0, errors.New("simulated broken pipe")
}

func TestRunOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"-events", "1", "-output", "-"}, brokenOutput{}, &stderr); code != 1 {
		t.Fatalf("expected output failure, got %d", code)
	}
	if strings.Contains(stderr.String(), "Generated") || !strings.Contains(stderr.String(), "broken pipe") {
		t.Fatalf("incorrect output failure message: %s", &stderr)
	}
}

func TestRunOutputParentIsFile(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-output", filepath.Join(parent, "events.jsonl")}, &stdout, &stderr); code != 1 {
		t.Fatalf("expected directory error, got %d", code)
	}
	if !strings.Contains(stderr.String(), "create output directory") {
		t.Fatalf("missing directory error: %s", &stderr)
	}
}
