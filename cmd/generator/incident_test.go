package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestIncidentCLI(t *testing.T) {
	var output, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-profile", "incident", "-events", "100", "-output", "-"}, &output, &stderr); code != 0 {
		t.Fatalf("incident generation failed: %s", &stderr)
	}
	if bytes.Count(output.Bytes(), []byte("\n")) != 100 {
		t.Fatal("incident CLI did not produce the requested record count")
	}
	for _, args := range [][]string{
		{"-profile", "unknown"}, {"-profile", "incident", "-events", "1"},
		{"-profile", "incident", "-interval", "1us"},
	} {
		output.Reset()
		stderr.Reset()
		args = append(args, "-output", "-")
		if code := run(context.Background(), args, &output, &stderr); code != 2 ||
			output.Len() != 0 || !strings.Contains(stderr.String(), "profile") {
			t.Fatalf("invalid incident arguments did not fail clearly: %v -> %d %s", args, code, &stderr)
		}
	}
}
