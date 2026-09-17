package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/release"
)

func verificationFixture(t *testing.T, target string) string {
	t.Helper()
	files := map[string][]byte{
		"RUN.txt": []byte("run"), "PROJECT-LICENSE.txt": []byte("unselected"),
		"web/dist/index.html": []byte("index"), "web/dist/assets/test.js": []byte("test"),
		"licenses/Go-LICENSE.txt": []byte("license"), "licenses/Go-PATENTS.txt": []byte("patents"),
		"licenses/react-LICENSE.txt": []byte("MIT"), "licenses/react-dom-LICENSE.txt": []byte("MIT"),
		"licenses/scheduler-LICENSE.txt": []byte("MIT"),
	}
	for _, name := range release.Commands {
		if target == "windows-amd64" {
			name += ".exe"
		}
		// Intentionally non-executable: verification must not launch payloads.
		files["bin/"+name] = []byte("not an executable")
	}
	manifest := release.Manifest{Schema: 1, Version: "0.14.0-preview.1", Target: target}
	for name, data := range files {
		mode := uint32(0644)
		if strings.HasPrefix(name, "bin/") {
			mode = 0755
		}
		manifest.Files = append(manifest.Files, release.Fingerprint(name, data, mode))
	}
	var err error
	files["manifest.json"], err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := release.WriteZIP(&archive, files); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "preview.zip")
	if err := os.WriteFile(path, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	checksum := release.Fingerprint("", archive.Bytes(), 0).SHA256 + "  preview.zip\n"
	if err := os.WriteFile(path+".sha256", []byte(checksum), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyOnlyBothTargetsWithoutExtractionOrExecution(t *testing.T) {
	for _, target := range []string{"windows-amd64", "linux-amd64"} {
		t.Run(target, func(t *testing.T) {
			archive := verificationFixture(t, target)
			var stdout, stderr bytes.Buffer
			if code := run([]string{"-archive", archive, "-verify-only"}, &stdout, &stderr); code != 0 {
				t.Fatalf("exit %d: %s", code, &stderr)
			}
			var result struct {
				Verified  bool             `json:"verified"`
				Extracted bool             `json:"extracted"`
				Executed  bool             `json:"executed"`
				Manifest  release.Manifest `json:"manifest"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.Verified || result.Extracted || result.Executed || result.Manifest.Target != target ||
				result.Manifest.Version != "0.14.0-preview.1" || len(result.Manifest.Files) != 15 || stderr.Len() != 0 {
				t.Fatalf("unexpected result: %s; stderr %s", &stdout, &stderr)
			}
			entries, err := os.ReadDir(filepath.Dir(archive))
			if err != nil || len(entries) != 2 {
				t.Fatalf("verification changed archive directory: %v %v", entries, err)
			}
			if err := os.WriteFile(archive+".sha256", []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			stdout.Reset()
			if code := run([]string{"-archive", archive, "-verify-only"}, &stdout, &stderr); code != 1 ||
				stdout.Len() != 0 || !strings.Contains(stderr.String(), "checksum mismatch") {
				t.Fatalf("tamper result: %d %s %s", code, &stdout, &stderr)
			}
		})
	}
}

func TestRunArgumentAndMissingArchiveErrors(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-archive", "missing.zip"}, {"-verify-only"},
		{"-archive", "missing.zip", "-verify-only", "-work-root", "."},
		{"-archive", "missing.zip", "-verify-only", "-browser-script", "test.mjs"},
		{"-archive", "missing.zip", "-verify-only", "extra"}, {"-unknown"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Errorf("%v: exit %d stdout %s stderr %s", args, code, &stdout, &stderr)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-archive", filepath.Join(t.TempDir(), "missing.zip"), "-verify-only"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Fatalf("missing archive: exit %d stdout %s stderr %s", code, &stdout, &stderr)
	}
	if code := run([]string{"-help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help: exit %d", code)
	}
}

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
