package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const fixture = `{"timestamp_us":1,"service":"api","duration_us":100,"status":200}
{"timestamp_us":2,"service":"api","duration_us":200,"status":500}
`

func datasetFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunMatchingEngines(t *testing.T) {
	path := datasetFile(t, fixture)
	var reports []report
	for _, engine := range []string{"row", "columnar", "indexed"} {
		var stdout, stderr bytes.Buffer
		args := []string{"-input", path, "-engine", engine, "-from-us", "1", "-to-us", "3", "-service", "api", "-status", "500"}
		if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("exit %d: %s", code, &stderr)
		}
		var result report
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Result.Aggregate.Count != 1 || result.Result.Aggregate.DurationSumUS != 200 ||
			result.Result.Stats.RowsExamined != 2 || result.Services != 1 ||
			(result.LoadMS != nil && *result.LoadMS < 0) ||
			(result.QueryMS != nil && *result.QueryMS < 0) || stderr.Len() != 0 {
			t.Fatalf("invalid query report: %+v, stderr=%s", result, &stderr)
		}
		if (result.LoadMS == nil || result.QueryMS == nil) && result.TimingNote == "" {
			t.Fatal("unresolved duration must include an explicit timing note")
		}
		reports = append(reports, result)
	}
	for _, result := range reports[1:] {
		if !reflect.DeepEqual(reports[0].Result.Aggregate, result.Result.Aggregate) {
			t.Fatal("CLI engines disagree")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != fixture {
		t.Fatal("query modified the input file")
	}
}

func TestRunInvalidArguments(t *testing.T) {
	tests := [][]string{
		{"-engine", "unknown"}, {"-max-events", "0"}, {"-max-events", "bad"},
		{"-from-us", "-1"}, {"-from-us", "5", "-to-us", "4"}, {"-to-us", "bad"},
		{"-to-us", "9223372036854775808"}, {"-status", "-1"}, {"-status", "99"},
		{"-status", "600"}, {"-status", "65536"}, {"-input", ""}, {"-service", " api"},
		{"-unknown"}, {"positional"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args = append([]string{"-input", filepath.Join(t.TempDir(), "absent.jsonl")}, args...)
			if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
				t.Fatalf("expected usage error before reading file, got %d: %s", code, &stderr)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatal("invalid arguments must report only on stderr")
			}
		})
	}
}

func TestRunInputFailures(t *testing.T) {
	tests := []struct {
		name, input string
		args        []string
	}{
		{"malformed second line", fixture + "broken", nil},
		{"event limit", fixture, []string{"-max-events", "1"}},
		{"unordered", strings.Replace(fixture, `"timestamp_us":2`, `"timestamp_us":0`, 1), nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"-input", datasetFile(t, test.input)}, test.args...)
			if code := run(context.Background(), args, &stdout, &stderr); code != 1 {
				t.Fatalf("expected input failure, got %d: %s", code, &stderr)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "line ") {
				t.Fatalf("expected contextual failure without partial report: %s", &stderr)
			}
		})
	}
}

func TestRunMissingFileAndCancellation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-input", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr); code != 1 {
		t.Fatalf("missing file returned %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := run(ctx, []string{"-input", datasetFile(t, fixture)}, &stdout, &stderr); code != 1 {
		t.Fatalf("cancellation returned %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatal("failure emitted a success-shaped result")
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-help"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stderr.String(), "Usage: query") || stdout.Len() != 0 {
		t.Fatalf("invalid help response: %d, %s", code, &stderr)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("simulated broken pipe") }

func TestRunOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"-input", datasetFile(t, fixture)}, brokenWriter{}, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "write result") {
		t.Fatalf("invalid writer failure: %d, %s", code, &stderr)
	}
}

func TestRunEmptyDataset(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-input", datasetFile(t, "")}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty dataset failed: %s", &stderr)
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Result.Aggregate.Count != 0 ||
		result.Result.Aggregate.MeanDurationUS != nil || result.Result.Stats.TotalRows != 0 {
		t.Fatalf("invalid empty dataset result: %s (%v)", &stdout, err)
	}
}

func TestDurationMillisecondsDoesNotClaimZeroLatency(t *testing.T) {
	if durationMilliseconds(0) != nil {
		t.Fatal("an unresolved clock reading must not be reported as zero latency")
	}
	if got := durationMilliseconds(1250 * time.Microsecond); got == nil || *got != 1.25 {
		t.Fatalf("expected exactly 1.25 milliseconds, got %v", got)
	}
}
