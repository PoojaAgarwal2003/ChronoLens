package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/benchmark"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/snapshot"
)

func TestSnapshotFormatFlagAndErrors(t *testing.T) {
	var encoded bytes.Buffer
	if _, err := snapshot.WriteJSONL(context.Background(), &encoded, strings.NewReader(fixture), 2); err != nil {
		t.Fatal(err)
	}
	path := datasetFile(t, encoded.String())
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), append(quickArgs(path), "-format", "snapshot"), &stdout, &stderr); code != 0 {
		t.Fatalf("snapshot benchmark failed: %d %s", code, &stderr)
	}
	var report benchmark.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Source.InputFormat != query.SnapshotFormat {
		t.Fatalf("missing format provenance: %+v (%v)", report.Source, err)
	}
	for _, format := range []string{"auto", "", "SNAPSHOT"} {
		stdout.Reset()
		stderr.Reset()
		if code := run(context.Background(), []string{"-input", "missing", "-format", format}, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
			t.Fatalf("invalid format %q returned %d", format, code)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), quickArgs(path), &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Fatal("default JSONL mode inferred a snapshot or exposed a partial report")
	}
}
