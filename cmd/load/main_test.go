package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("report disk failure") }

func TestReportWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	if run(ctx, []string{"-duration", "1ms"}, failingWriter{}, &stderr) != 1 ||
		!strings.Contains(stderr.String(), "report disk failure") {
		t.Fatalf("write failure hidden: %s", &stderr)
	}
}

func TestCLIValidation(t *testing.T) {
	for _, args := range [][]string{{"-rate", "0"}, {"-duration", "2m"}, {"-payload", "null"}, {"extra"}, {"-target", "http://localhost:80/api/query"}} {
		var out, err bytes.Buffer
		if run(context.Background(), args, &out, &err) != 2 || err.Len() == 0 || out.Len() != 0 {
			t.Fatalf("%v %s %s", args, &out, &err)
		}
	}
}

func TestReportSurvivesCancellationAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, err bytes.Buffer
	args := []string{"-output", path, "-duration", "1ms"}
	if run(ctx, args, &out, &err) != 1 {
		t.Fatal("cancelled run must be nonzero")
	}
	before, e := os.ReadFile(path)
	if e != nil || !strings.Contains(string(before), `"skipped_cancelled"`) {
		t.Fatalf("%s %v", before, e)
	}
	if run(ctx, args, &out, &err) != 1 {
		t.Fatal("must refuse overwrite")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("overwrote report")
	}
}
