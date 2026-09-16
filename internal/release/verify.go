package release

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Verify checks transfer checksum, entry safety/bounds, exact manifest coverage,
// payload hashes and the six expected executables before any extraction.
// A matching checksum is not an authenticity signature.
func Verify(archive string) (*Manifest, map[string][]byte, error) {
	entry, err := os.Lstat(archive)
	if err != nil || !entry.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("archive must be a regular non-symlink file: %v", err)
	}
	check, err := os.Lstat(archive + ".sha256")
	if err != nil || !check.Mode().IsRegular() || check.Size() > 1024 {
		return nil, nil, fmt.Errorf("missing/invalid checksum sidecar: %v", err)
	}
	sidecar, err := ReadRegular(filepath.Dir(archive), filepath.Base(archive)+".sha256")
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, nil, errors.New("archive is not a bounded regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, nil, err
	}
	want := hex.EncodeToString(h.Sum(nil)) + "  " + filepath.Base(archive) + "\n"
	if string(sidecar) != want {
		return nil, nil, errors.New("archive checksum mismatch")
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		return nil, nil, err
	}
	if len(z.File) > MaxFiles {
		return nil, nil, errors.New("too many archive entries")
	}
	files, seen, total := map[string][]byte{}, map[string]bool{}, uint64(0)
	for _, entry := range z.File {
		if !SafeName(entry.Name) || seen[strings.ToLower(entry.Name)] ||
			!entry.Mode().IsRegular() || entry.Mode() & ^os.ModePerm != 0 ||
			uint32(entry.Mode().Perm()) != fileMode(entry.Name) ||
			entry.UncompressedSize64 > MaxFileBytes {
			return nil, nil, fmt.Errorf("unsafe archive entry: %s", entry.Name)
		}
		seen[strings.ToLower(entry.Name)] = true
		total += entry.UncompressedSize64
		if total > MaxBytes {
			return nil, nil, errors.New("archive exceeds expanded size limit")
		}
		r, err := entry.Open()
		if err != nil {
			return nil, nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, int64(entry.UncompressedSize64)+1))
		err = errors.Join(err, r.Close())
		if err != nil || uint64(len(b)) != entry.UncompressedSize64 {
			return nil, nil, fmt.Errorf("invalid ZIP data %s: %v", entry.Name, err)
		}
		files[entry.Name] = b
	}
	var manifest Manifest
	if len(files["manifest.json"]) > 4<<20 {
		return nil, nil, errors.New("manifest exceeds limit")
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		return nil, nil, err
	}
	if manifest.Schema != 1 || len(manifest.Version) > 64 || !versionPattern.MatchString(manifest.Version) ||
		(manifest.Target != "windows-amd64" && manifest.Target != "linux-amd64") ||
		len(manifest.Files)+1 != len(files) {
		return nil, nil, errors.New("invalid manifest schema, target, version or coverage")
	}
	listed := map[string]bool{}
	for _, record := range manifest.Files {
		b, ok := files[record.Path]
		if !ok || record.Path == "manifest.json" || listed[record.Path] ||
			record != Fingerprint(record.Path, b, fileMode(record.Path)) {
			return nil, nil, fmt.Errorf("manifest hash/size/mode mismatch: %s", record.Path)
		}
		listed[record.Path] = true
	}
	required := []string{"RUN.txt", "PROJECT-LICENSE.txt", "web/dist/index.html", "licenses/Go-LICENSE.txt",
		"licenses/Go-PATENTS.txt", "licenses/react-LICENSE.txt", "licenses/react-dom-LICENSE.txt", "licenses/scheduler-LICENSE.txt"}
	for _, n := range Commands {
		if manifest.Target == "windows-amd64" {
			n += ".exe"
		}
		required = append(required, "bin/"+n)
	}
	for _, n := range required {
		if len(files[n]) == 0 {
			return nil, nil, fmt.Errorf("required package file missing: %s", n)
		}
	}
	hasJS := false
	for n := range files {
		if n == "manifest.json" {
			continue
		}
		allowed := false
		for _, r := range required {
			allowed = allowed || n == r
		}
		allowed = allowed || strings.HasPrefix(n, "web/dist/assets/") &&
			(strings.HasSuffix(n, ".js") || strings.HasSuffix(n, ".css") || strings.HasSuffix(n, ".svg"))
		hasJS = hasJS || strings.HasPrefix(n, "web/dist/assets/") && strings.HasSuffix(n, ".js")
		if !allowed {
			return nil, nil, fmt.Errorf("unexpected payload file: %s", n)
		}
	}
	if !hasJS {
		return nil, nil, errors.New("package has no frontend JavaScript")
	}
	return &manifest, files, nil
}

// Extract requires a nonexistent destination. Failures remove only the directory
// exclusively created here. It never follows archive-provided links.
func Extract(destination string, files map[string][]byte) (err error) {
	if err = os.Mkdir(destination, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(destination))
		}
	}()
	for n, b := range files {
		if err = put(destination, n, b, os.FileMode(fileMode(n))); err != nil {
			return err
		}
	}
	return nil
}
