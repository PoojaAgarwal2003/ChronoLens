package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/benchmark"
)

const fixture = `{"timestamp_us":1,"service":"api","duration_us":100,"status":200}
{"timestamp_us":2,"service":"api","duration_us":200,"status":500}
`

func datasetFile(t *testing.T, content string) string {
	t.Helper()
	file, err := os.CreateTemp(".", ".bench-cli-fixture-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(file.Name()); err != nil {
			t.Error(err)
		}
	})
	_, writeErr := io.WriteString(file, content)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}

func quickArgs(path string) []string {
	return []string{"-input", path, "-samples", "1", "-window", "1ns", "-max-iterations", "3"}
}

func TestRunJSONOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), quickArgs(datasetFile(t, fixture)), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	decoder := json.NewDecoder(&stdout)
	var report benchmark.Report
	if err := decoder.Decode(&report); err != nil || report.SchemaVersion != 1 || len(report.Engines) != 3 {
		t.Fatalf("incorrect JSON report: %+v, %v", report, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || stderr.Len() != 0 {
		t.Fatal("stdout must contain exactly one JSON report; successful run should be quiet")
	}
}

func TestRunInvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-input", ""}, {"-max-events", "0"}, {"-max-events", "-1"}, {"-max-events", "100000001"},
		{"-max-events", "bad"}, {"-samples", "0"}, {"-samples", "101"}, {"-samples", "x"},
		{"-window", "0"}, {"-window", "-1ns"}, {"-window", "61s"}, {"-window", "bad"},
		{"-max-iterations", "2"}, {"-max-iterations", "100000001"}, {"-timeout", "0"},
		{"-timeout", "25h"}, {"-timeout", "-1s"}, {"-timeout", "bad"}, {"-unknown"}, {"positional"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args = append([]string{"-input", ".missing-bench-input.jsonl"}, args...)
			if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("invalid flags: exit %d, stdout=%s, stderr=%s", code, &stdout, &stderr)
			}
		})
	}
}

func TestRunFailuresAndHelp(t *testing.T) {
	for _, content := range []string{"", fixture + "broken", strings.Replace(fixture, `"timestamp_us":2`, `"timestamp_us":0`, 1)} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), quickArgs(datasetFile(t, content)), &stdout, &stderr); code != 1 ||
			stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("input error returned %d: %s", code, &stderr)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), append(quickArgs(datasetFile(t, fixture)), "-max-events", "1"), &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "max-events") {
		t.Fatalf("event limit returned %d: %s", code, &stderr)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), quickArgs(".missing-bench-input.jsonl"), &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Fatalf("missing input returned %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := run(ctx, quickArgs(".missing-bench-input.jsonl"), &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("cancellation returned %d: %s", code, &stderr)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"-help"}, &stdout, &stderr); code != 0 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "Usage: bench") {
		t.Fatalf("incorrect help: %d, %s", code, &stderr)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("simulated broken pipe") }

func TestRunOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), quickArgs(datasetFile(t, fixture)), brokenWriter{}, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "write report") {
		t.Fatalf("output failure: %d, %s", code, &stderr)
	}
}
