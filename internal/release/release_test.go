package release

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestValidation(t *testing.T) {
	valid := Config{Root: ".", Output: "preview.zip", Version: "0.11.0-preview.1", Target: "windows-amd64"}
	if err := Validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"", "latest", "../0.11.0", "0.11.0", "v0.11.0-preview", "00.11.0-preview", "0.11.0-preview.01", "0.11.0-preview/evil", strings.Repeat("1", 65) + ".0.0-preview"} {
		c := valid
		c.Version = v
		if Validate(c) == nil {
			t.Errorf("accepted version %q", v)
		}
	}
	for _, v := range []string{"linux-arm64", "../linux-amd64", "windows/amd64", ""} {
		c := valid
		c.Target = v
		if Validate(c) == nil {
			t.Errorf("accepted target %q", v)
		}
	}
	for _, n := range []string{"", "../escape", "/abs", "C:/abs", `a\b`, "a//b", "a/./b", "a/../b", "a:", "a.", "NUL.txt", "com1", "a/space name"} {
		if SafeName(n) {
			t.Errorf("accepted unsafe name %q", n)
		}
	}
}

func TestZIPDeterministicAndNormalized(t *testing.T) {
	files := map[string][]byte{"web/dist/index.html": []byte("index"), "bin/server": []byte("binary")}
	var a, b bytes.Buffer
	if err := WriteZIP(&a, files); err != nil {
		t.Fatal(err)
	}
	if err := WriteZIP(&b, map[string][]byte{"bin/server": []byte("binary"), "web/dist/index.html": []byte("index")}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("archive bytes differ")
	}
	z, err := zip.NewReader(bytes.NewReader(a.Bytes()), int64(a.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range z.File {
		names = append(names, f.Name)
		if f.Modified.Year() != 1980 || uint32(f.Mode().Perm()) != fileMode(f.Name) {
			t.Fatalf("not normalized: %+v", f.FileHeader)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Fatal("unsorted entries")
	}
	for _, files := range []map[string][]byte{
		{"../escape": nil}, {"a": nil, "A": nil}, {"bin/server": nil, "bin/../server": nil},
	} {
		if WriteZIP(&bytes.Buffer{}, files) == nil {
			t.Fatal("accepted unsafe archive")
		}
	}
}

func fixture(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{"RUN.txt": []byte("run"), "PROJECT-LICENSE.txt": []byte("unselected"),
		"web/dist/index.html":     []byte(`<script src="/assets/test.js"></script>`),
		"web/dist/assets/test.js": []byte("test"),
		"licenses/Go-LICENSE.txt": []byte("license"), "licenses/Go-PATENTS.txt": []byte("patents"),
		"licenses/react-LICENSE.txt": []byte("MIT"), "licenses/react-dom-LICENSE.txt": []byte("MIT"),
		"licenses/scheduler-LICENSE.txt": []byte("MIT")}
	for _, n := range Commands {
		files["bin/"+n] = []byte("fixture binary")
	}
	m := Manifest{Schema: 1, Version: "0.11.0-preview", Target: "linux-amd64"}
	for n, b := range files {
		m.Files = append(m.Files, Fingerprint(n, b, fileMode(n)))
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	files["manifest.json"] = b
	return files
}

func archiveFixture(t *testing.T, files map[string][]byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.zip")
	var b bytes.Buffer
	if err := WriteZIP(&b, files); err != nil {
		t.Fatal(err)
	}
	writeChecksum(t, p, b.Bytes())
	return p
}

func writeChecksum(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if err := os.WriteFile(p+".sha256", []byte(hex.EncodeToString(sum[:])+"  "+filepath.Base(p)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyExtractAndRefuseOverwrite(t *testing.T) {
	files := fixture(t)
	p := archiveFixture(t, files)
	m, got, err := Verify(p)
	if err != nil || m.Target != "linux-amd64" || !reflect.DeepEqual(got, files) {
		t.Fatalf("verify: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "app")
	if err := Extract(dest, got); err != nil {
		t.Fatal(err)
	}
	if err := Extract(dest, got); err == nil {
		t.Fatal("overwrote existing destination")
	}
	for n, want := range files {
		b, err := ReadRegular(dest, n)
		if err != nil || !bytes.Equal(b, want) {
			t.Fatalf("extraction differs: %s %v", n, err)
		}
	}
}

func TestTamperMissingAndInvalidArchive(t *testing.T) {
	t.Run("missing archive", func(t *testing.T) {
		if _, _, err := Verify(filepath.Join(t.TempDir(), "missing.zip")); err == nil {
			t.Fatal("accepted missing archive")
		}
	})
	t.Run("missing checksum", func(t *testing.T) {
		p := archiveFixture(t, fixture(t))
		os.Remove(p + ".sha256")
		if _, _, err := Verify(p); err == nil {
			t.Fatal("accepted missing checksum")
		}
	})
	t.Run("checksum tamper", func(t *testing.T) {
		p := archiveFixture(t, fixture(t))
		os.WriteFile(p, []byte("tamper"), 0644)
		if _, _, err := Verify(p); err == nil {
			t.Fatal("accepted checksum mismatch")
		}
	})
	for _, name := range []string{"tampered payload", "missing payload", "unlisted payload", "invalid manifest", "invalid zip"} {
		t.Run(name, func(t *testing.T) {
			files := fixture(t)
			switch name {
			case "tampered payload":
				files["bin/server"] = []byte("tamper")
			case "missing payload":
				delete(files, "web/dist/index.html")
			case "unlisted payload":
				files["secret.txt"] = []byte("not permitted")
			case "invalid manifest":
				files["manifest.json"] = []byte("{")
			}
			p := archiveFixture(t, files)
			if name == "invalid zip" {
				writeChecksum(t, p, []byte("not a ZIP"))
			}
			if _, _, err := Verify(p); err == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
}

func TestRejectUntrustedZIPEntries(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", `a\b`, "symlink", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			h := &zip.FileHeader{Name: name}
			h.SetMode(0644)
			if name == "symlink" {
				h.SetMode(os.ModeSymlink | 0777)
			}
			w, _ := z.CreateHeader(h)
			w.Write([]byte("target"))
			if name == "duplicate" {
				z.CreateHeader(h)
			}
			z.Close()
			p := filepath.Join(t.TempDir(), "unsafe.zip")
			writeChecksum(t, p, b.Bytes())
			if _, _, err := Verify(p); err == nil {
				t.Fatal("accepted untrusted ZIP entry")
			}
		})
	}
}

func TestReadRejectsSymlinksAndMissingAssets(t *testing.T) {
	root := t.TempDir()
	if _, err := collect(filepath.Join(root, "missing-dist")); err == nil {
		t.Fatal("accepted missing assets")
	}
	if err := os.WriteFile(filepath.Join(root, "regular"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "regular"), filepath.Join(root, "link")); err != nil {
		t.Skipf("host does not grant symlink creation: %v", err)
	}
	if _, err := ReadRegular(root, "link"); err == nil {
		t.Fatal("read symlink")
	}
	if _, err := collect(root); err == nil {
		t.Fatal("collected symlink")
	}
	parent := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(root, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegular(parent, "regular"); err == nil {
		t.Fatal("read through symlinked root directory")
	}
}

func TestPublishNoClobberAndFailureCleanup(t *testing.T) {
	root := t.TempDir()
	a, s, out := filepath.Join(root, "a"), filepath.Join(root, "s"), filepath.Join(root, "out.zip")
	os.WriteFile(a, []byte("archive"), 0644)
	os.WriteFile(s, []byte("checksum"), 0644)
	if err := Publish(a, s, out, os.Link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(a, s, out, os.Link); err == nil {
		t.Fatal("overwrote output")
	}
	if b, _ := os.ReadFile(out); string(b) != "archive" {
		t.Fatal("modified output")
	}
	os.Remove(out + ".sha256")
	if err := Publish(a, s, out, os.Link); err == nil {
		t.Fatal("overwrote preexisting archive")
	}
	if _, err := os.Lstat(out + ".sha256"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed publication left checksum")
	}
	os.Remove(out)
	if err := Publish(a, s, out, func(string, string) error { return errors.New("unsupported hard links") }); err == nil {
		t.Fatal("silently fell back from hard links")
	}
}

func TestFailedBuildLeavesNoPublishedOutput(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(root, "preview.zip")
	_, err := Build(ctx, Config{Root: root, Output: out, Version: "0.11.0-preview", Target: "linux-amd64"})
	if err == nil {
		t.Fatal("canceled build succeeded")
	}

	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed build leaked staging or outputs: %v %v", entries, err)
	}
}

func TestBoundsAndExtractionRollback(t *testing.T) {
	var b boundedBuffer
	if _, err := b.Write(make([]byte, 1<<20+1)); err == nil {
		t.Fatal("accepted excessive child output")
	}
	files := map[string][]byte{}
	for i := range MaxFiles + 1 {
		files[strings.Repeat("a", i/100+1)+string(rune('a'+i%100))] = nil
	}
	if err := WriteZIP(&bytes.Buffer{}, files); err == nil {
		t.Fatal("accepted excessive archive entry count")
	}
	root := t.TempDir()
	p := filepath.Join(root, "large")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := ReadRegular(root, "large"); err == nil {
		t.Fatal("accepted oversized file")
	}
	dest := filepath.Join(root, "app")
	if err := Extract(dest, map[string][]byte{"../escape": []byte("bad")}); err == nil {
		t.Fatal("extracted unsafe path")
	}
	if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed extraction leaked destination")
	}
}
