package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/snapshot"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

const fixture = `{"timestamp_us":0,"service":"api","duration_us":100,"status":200}
{"timestamp_us":1,"service":"worker","duration_us":200,"status":500}
`

func paths(t *testing.T, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.jsonl")
	if err := os.WriteFile(input, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return input, filepath.Join(dir, "output.clens")
}

func noPrivateFiles(t *testing.T, output string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(output), ".chronolens-pack-*.tmp"))
	if err != nil || len(files) != 0 {
		t.Fatalf("private staging files remain: %v, %v", files, err)
	}
}

func TestPackReportAndSnapshot(t *testing.T) {
	input, output := paths(t, fixture)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-input", input, "-output", output}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Events != 2 || result.Blocks != 1 || result.SourceBytes != int64(len(fixture)) ||
		result.SourceSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(fixture))) ||
		result.Input != "input.jsonl" || result.Output != "output.clens" || stderr.Len() != 0 {
		t.Fatalf("incorrect report: %+v, %s", result, &stderr)
	}
	data, err := os.ReadFile(output)
	if err != nil || int64(len(data)) != result.SnapshotBytes {
		t.Fatal("incorrect snapshot size")
	}
	var events []telemetry.Event
	_, err = snapshot.Read(context.Background(), bytes.NewReader(data), 2, func(e telemetry.Event) error {
		events = append(events, e)
		return nil
	})
	if err != nil || !reflect.DeepEqual(events, []telemetry.Event{{TimestampUS: 0, Service: "api", DurationUS: 100, Status: 200}, {TimestampUS: 1, Service: "worker", DurationUS: 200, Status: 500}}) {
		t.Fatalf("roundtrip failed: %+v, %v", events, err)
	}
	noPrivateFiles(t, output)
}

func TestPackFailureCleanupAndNoOverwrite(t *testing.T) {
	for _, content := range []string{"invalid", fixture + "invalid", strings.Replace(fixture, `"timestamp_us":1`, `"timestamp_us":-1`, 1)} {
		input, output := paths(t, content)
		if _, err := pack(context.Background(), input, output, 10, os.Link); err == nil {
			t.Fatal("malformed source succeeded")
		}
		if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("published partial output: %v", err)
		}
		noPrivateFiles(t, output)
	}
	input, output := paths(t, fixture)
	if _, err := pack(context.Background(), input, output, 1, os.Link); err == nil {
		t.Fatal("row limit ignored")
	}
	noPrivateFiles(t, output)
	if err := os.WriteFile(output, []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pack(context.Background(), input, output, 2, os.Link); err == nil {
		t.Fatal("existing destination accepted")
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "preserve me" {
		t.Fatal("existing output changed")
	}
}

func TestConcurrentPublisherWinsWithoutReplacement(t *testing.T) {
	input, output := paths(t, fixture)
	_, err := pack(context.Background(), input, output, 2, func(temp, final string) error {
		if err := os.WriteFile(final, []byte("other publisher"), 0o600); err != nil {
			t.Fatal(err)
		}
		return os.Link(temp, final)
	})
	if err == nil {
		t.Fatal("destination race reported success")
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil || string(data) != "other publisher" {
		t.Fatal("concurrent output was replaced")
	}
	noPrivateFiles(t, output)
}

func TestPublicationFailureAndCancellation(t *testing.T) {
	input, output := paths(t, fixture)
	want := errors.New("unsupported filesystem")
	if _, err := pack(context.Background(), input, output, 2, func(string, string) error { return want }); !errors.Is(err, want) {
		t.Fatalf("publication error hidden: %v", err)
	}
	noPrivateFiles(t, output)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pack(ctx, input, output, 2, os.Link); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled conversion did not fail")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed conversion published a file")
	}
}

func TestInvalidArgumentsAndHelp(t *testing.T) {
	for _, args := range [][]string{{"-input", ""}, {"-output", "-"}, {"-input", "-"}, {"-max-events", "0"}, {"-max-events", "100000001"}, {"-max-events", "bad"}, {"-unknown"}, {"extra"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("invalid arguments accepted: %v -> %d", args, code)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-help"}, &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Fatal("help failed")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReportFailurePreservesPublishedSnapshot(t *testing.T) {
	input, output := paths(t, fixture)
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"-input", input, "-output", output}, brokenWriter{}, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "already published") {
		t.Fatalf("report failure not explained: %d %s", code, &stderr)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal("valid published snapshot removed after report failure")
	}
	noPrivateFiles(t, output)
}
