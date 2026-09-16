package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmokeRejectsRepositoryWorkspace(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := smoke(context.Background(), "missing.zip", cwd, ""); err == nil {
		t.Fatal("accepted non-isolated workspace")
	}
}

func TestSmokeMissingArchiveDoesNotCreateWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := smoke(context.Background(), filepath.Join(root, "missing.zip"), root, ""); err == nil {
		t.Fatal("accepted missing archive")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed verification created workspace: %v %v", entries, err)
	}
}
