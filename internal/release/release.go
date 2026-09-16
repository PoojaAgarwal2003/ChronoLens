// Package release builds bounded, deterministic local-preview ZIP archives.
package release

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const MaxBytes = 512 << 20
const MaxFileBytes = 128 << 20
const MaxFiles = 4096

var Commands = []string{"generator", "pack", "query", "bench", "load", "server"}
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-preview(?:\.(0|[1-9][0-9]*))?$`)

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type Dependency struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Integrity string `json:"lockfile_integrity"`
	License   string `json:"license"`
}

type Manifest struct {
	Schema         int               `json:"schema"`
	Version        string            `json:"version"`
	Target         string            `json:"target"`
	Classification string            `json:"classification"`
	Commit         string            `json:"git_commit"`
	Dirty          bool              `json:"git_dirty"`
	Tools          map[string]string `json:"tools"`
	Build          []string          `json:"go_build_flags"`
	Frontend       string            `json:"frontend_provenance"`
	Dependencies   []Dependency      `json:"frontend_production_dependencies"`
	Sources        []File            `json:"source_fingerprints"`
	Files          []File            `json:"files"`
}

type Config struct {
	Root, Output, Version, Target string
	AllowDirty                    bool
}

func Validate(c Config) error {
	if len(c.Version) > 64 || !versionPattern.MatchString(c.Version) {
		return errors.New("version must be N.N.N-preview[.N] (local preview only)")
	}
	if c.Target != "windows-amd64" && c.Target != "linux-amd64" {
		return errors.New("target must be windows-amd64 or linux-amd64")
	}
	if c.Root == "" || c.Output == "" || !strings.HasSuffix(c.Output, ".zip") || !SafeName(filepath.Base(c.Output)) {
		return errors.New("root and a new .zip output path are required")
	}
	return nil
}

func SafeName(name string) bool {
	if name == "" || len(name) > 240 || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == ".." || strings.HasSuffix(part, ".") {
			return false
		}
		base := strings.ToUpper(strings.Split(part, ".")[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			regexp.MustCompile(`^(COM|LPT)[0-9]$`).MatchString(base) {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.@", c)) {
				return false
			}
		}
	}
	return true
}

// ReadRegular rejects links in every path component, not only the final file.
func ReadRegular(root, name string) ([]byte, error) {
	return readRegular(root, name, false)
}

// SDK installers may link GOROOT, including Windows junctions. Only the trusted
// installation root may follow links; individual notice files still may not.
func readToolchainNotice(root, name string) ([]byte, error) {
	return readRegular(root, name, true)
}

func readRegular(root, name string, trustedRoot bool) ([]byte, error) {
	if !SafeName(name) {
		return nil, fmt.Errorf("unsafe path %q", name)
	}
	current, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// Check root ancestors too, including Windows junctions exposed as ModeSymlink.
	for p := current; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if trustedRoot {
			info, err = os.Stat(p)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe directory %s: %v", p, err)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) {
			return nil, fmt.Errorf("link or non-directory in %s", current)
		}
		if i == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() > MaxFileBytes) {
			return nil, fmt.Errorf("not a bounded regular file: %s", current)
		}
	}
	f, err := os.Open(current)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if len(b) > MaxFileBytes {
		return nil, errors.New("file exceeds size limit")
	}
	return b, err
}

func Fingerprint(name string, b []byte, mode uint32) File {
	sum := sha256.Sum256(b)
	return File{name, hex.EncodeToString(sum[:]), int64(len(b)), mode}
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("command output exceeds 1 MiB")
	}
	return b.Buffer.Write(p)
}

func Command(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("%s %v: %w\n%s", name, args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func sourceName(n string) bool {
	return n == "go.mod" || n == "web/package.json" || n == "web/package-lock.json" ||
		n == "web/index.html" || n == "web/vite.config.ts" || n == "web/tsconfig.json" ||
		strings.HasPrefix(n, "web/src/") ||
		(strings.HasPrefix(n, "cmd/") || strings.HasPrefix(n, "internal/")) && strings.HasSuffix(n, ".go")
}

func sources(ctx context.Context, root string) ([]File, map[string][]byte, error) {
	list, err := Command(ctx, root, nil, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, err
	}
	names := strings.Split(strings.TrimSuffix(string(list), "\x00"), "\x00")
	sort.Strings(names)
	result, contents := []File{}, map[string][]byte{}
	total := 0
	for _, n := range names {
		if !sourceName(n) {
			continue
		}
		b, err := ReadRegular(root, n)
		if err != nil {
			return nil, nil, err
		}
		total += len(b)
		if total > MaxBytes || len(result) >= MaxFiles {
			return nil, nil, errors.New("source exceeds bounds")
		}
		result = append(result, Fingerprint(n, b, 0644))
		contents[n] = b
	}
	if len(contents["go.mod"]) == 0 || len(contents["web/package-lock.json"]) == 0 {
		return nil, nil, errors.New("missing source go.mod or frontend lockfile")
	}
	return result, contents, nil
}

func put(root, name string, b []byte, mode fs.FileMode) error {
	if !SafeName(name) {
		return errors.New("unsafe output name")
	}
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return errors.Join(err, f.Close())
}

func collect(root string) (map[string][]byte, error) {
	result := map[string][]byte{}
	total, entries := 0, 0
	var walk func(string) error
	walk = func(relative string) error {
		p := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe asset directory %s: %v", p, err)
		}
		dir, err := os.Open(p)
		if err != nil {
			return err
		}
		children, readErr := dir.ReadDir(MaxFiles - entries + 1)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return errors.Join(readErr, closeErr)
		}
		if closeErr != nil {
			return closeErr
		}
		entries += len(children)
		if entries > MaxFiles {
			return errors.New("asset tree exceeds entry limit")
		}
		for _, child := range children {
			n := path.Join(relative, child.Name())
			if !SafeName(n) || child.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe asset entry: %s", n)
			}
			if child.IsDir() {
				if err := walk(n); err != nil {
					return err
				}
				continue
			}
			b, err := ReadRegular(root, n)
			if err != nil {
				return err
			}
			total += len(b)
			if total > MaxBytes {
				return errors.New("assets exceed byte limit")
			}
			result[n] = b
		}
		return nil
	}
	return result, walk("")
}

// WriteZIP normalizes timestamps, ordering and modes independently of host metadata.
func WriteZIP(w io.Writer, files map[string][]byte) error {
	if len(files) > MaxFiles {
		return errors.New("too many archive files")
	}
	names, seen, total := []string{}, map[string]bool{}, 0
	for n, b := range files {
		if !SafeName(n) || seen[strings.ToLower(n)] {
			return fmt.Errorf("unsafe or colliding archive path: %s", n)
		}
		seen[strings.ToLower(n)] = true
		total += len(b)
		if len(b) > MaxFileBytes || total > MaxBytes {
			return errors.New("archive exceeds size bounds")
		}
		names = append(names, n)
	}
	sort.Strings(names)
	z := zip.NewWriter(w)
	for _, n := range names {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		h.SetMode(fs.FileMode(fileMode(n)))
		f, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := f.Write(files[n]); err != nil {
			return err
		}
	}
	return z.Close()
}

func fileMode(n string) uint32 {
	if strings.HasPrefix(n, "bin/") {
		return 0755
	}
	return 0644
}

// Publish uses exclusive hard links. Unsupported filesystems fail closed; no
// rename/copy fallback can replace another invocation's output.
func Publish(archive, checksum, output string, link func(string, string) error) error {
	if err := link(checksum, output+".sha256"); err != nil {
		return err
	}
	if err := link(archive, output); err != nil {
		return errors.Join(err, os.Remove(output+".sha256"))
	}
	return nil
}

func Build(ctx context.Context, c Config) (sum string, resultErr error) {
	if err := Validate(c); err != nil {
		return "", err
	}
	root, err := filepath.Abs(c.Root)
	if err != nil {
		return "", err
	}
	output, err := filepath.Abs(c.Output)
	if err != nil {
		return "", err
	}
	for _, p := range []string{output, output + ".sha256"} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("output exists or is inaccessible: %s", p)
		}
	}
	// Output parent must already exist; never create user-selected directory trees.
	stage, err := os.MkdirTemp(filepath.Dir(output), ".chronolens-release-")
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(stage)) }()
	manifest := Manifest{Schema: 1, Version: c.Version, Target: c.Target,
		Classification: "unsigned local preview; project license unselected; not a public redistribution grant",
		Tools:          map[string]string{}, Build: []string{"-trimpath", "-buildvcs=false", "-ldflags=-buildid=", "CGO_ENABLED=0", "GOAMD64=v1", "GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOEXPERIMENT=", "GOFIPS140=off"},
		Frontend: "npm run build executed against fingerprinted working-tree inputs; installed dependencies checked against lockfile versions; no cross-host reproducibility claim"}
	commit, err := Command(ctx, root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	manifest.Commit = strings.TrimSpace(string(commit))
	status, err := Command(ctx, root, nil, "git", "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return "", err
	}
	manifest.Dirty = len(status) != 0
	if manifest.Dirty && !c.AllowDirty {
		return "", errors.New("dirty source tree: explicitly pass -allow-dirty for local preview")
	}
	manifest.Sources, _, err = sources(ctx, root)
	if err != nil {
		return "", err
	}
	envFiles, err := filepath.Glob(filepath.Join(root, "web", ".env*"))
	if err != nil {
		return "", err
	}
	for _, p := range envFiles {
		if !strings.HasSuffix(p, ".example") {
			return "", errors.New("frontend .env files are forbidden in preview builds; use a clean trusted checkout")
		}
	}
	for _, e := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(e), "VITE_") {
			return "", errors.New("VITE_* environment overrides are forbidden in preview builds")
		}
	}
	for name, args := range map[string][]string{"go": {"version"}, "node": {"--version"}, "npm": {"--version"}} {
		executable := name
		if name == "npm" && runtime.GOOS == "windows" {
			executable = "npm.cmd"
		}
		var toolEnv []string
		if name == "go" {
			toolEnv = []string{"GOTOOLCHAIN=local", "GOENV=off"}
		}
		b, err := Command(ctx, root, toolEnv, executable, args...)
		if err != nil {
			return "", err
		}
		manifest.Tools[name] = strings.TrimSpace(string(b))
	}
	for name, script := range map[string]string{"vite": "web/node_modules/vite/bin/vite.js", "typescript": "web/node_modules/typescript/bin/tsc"} {
		if _, err := ReadRegular(root, script); err != nil {
			return "", err
		}
		b, err := Command(ctx, root, nil, "node", filepath.Join(root, filepath.FromSlash(script)), "--version")
		if err != nil {
			return "", err
		}
		manifest.Tools[name] = strings.TrimSpace(string(b))
	}
	npm := "npm"
	if runtime.GOOS == "windows" {
		npm = "npm.cmd"
	}
	if _, err := Command(ctx, filepath.Join(root, "web"), nil, npm, "run", "build"); err != nil {
		return "", err
	}
	after, contents, err := sources(ctx, root)
	if err != nil {
		return "", err
	}
	beforeJSON, _ := json.Marshal(manifest.Sources)
	afterJSON, _ := json.Marshal(after)
	if !bytes.Equal(beforeJSON, afterJSON) {
		return "", errors.New("source changed during frontend build; retry from stable inputs")
	}
	buildRoot := filepath.Join(stage, "source")
	for n, b := range contents {
		if n == "go.mod" || strings.HasSuffix(n, ".go") {
			if err := put(buildRoot, n, b, 0644); err != nil {
				return "", err
			}
		}
	}
	files := map[string][]byte{"RUN.txt": []byte(runInstructions), "PROJECT-LICENSE.txt": []byte(projectLicense)}
	assets, err := collect(filepath.Join(root, "web", "dist"))
	if err != nil {
		return "", err
	}
	if len(assets["index.html"]) == 0 {
		return "", errors.New("missing built frontend index.html")
	}
	js := false
	for n, b := range assets {
		if n != "index.html" && !(strings.HasPrefix(n, "assets/") &&
			(strings.HasSuffix(n, ".js") || strings.HasSuffix(n, ".css") || strings.HasSuffix(n, ".svg"))) {
			return "", fmt.Errorf("unexpected frontend asset: %s", n)
		}
		js = js || strings.HasSuffix(n, ".js")
		files["web/dist/"+n] = b
	}
	if !js {
		return "", errors.New("missing built frontend JavaScript")
	}
	var lock struct {
		Packages map[string]struct {
			Version, Integrity, License string
			Dev                         bool
		}
	}
	if err := json.Unmarshal(contents["web/package-lock.json"], &lock); err != nil {
		return "", err
	}
	for n, entry := range lock.Packages {
		if n != "" && !entry.Dev && n != "node_modules/react" && n != "node_modules/react-dom" && n != "node_modules/scheduler" {
			return "", fmt.Errorf("unreviewed production dependency: %s", n)
		}
	}
	for _, n := range []string{"react", "react-dom", "scheduler"} {
		entry, ok := lock.Packages["node_modules/"+n]
		if !ok || entry.Version == "" || entry.Integrity == "" || entry.License != "MIT" {
			return "", fmt.Errorf("missing/unexpected locked dependency: %s", n)
		}
		pkg, err := ReadRegular(root, "web/node_modules/"+n+"/package.json")
		if err != nil {
			return "", err
		}
		var installed struct{ Version string }
		if err := json.Unmarshal(pkg, &installed); err != nil || installed.Version != entry.Version {
			return "", fmt.Errorf("installed dependency differs from lock: %s", n)
		}
		license, err := ReadRegular(root, "web/node_modules/"+n+"/LICENSE")
		if err != nil || len(license) == 0 {
			return "", fmt.Errorf("missing dependency license %s: %v", n, err)
		}
		files["licenses/"+n+"-LICENSE.txt"] = license
		manifest.Dependencies = append(manifest.Dependencies, Dependency{n, entry.Version, entry.Integrity, entry.License})
	}
	goRoot, err := Command(ctx, root, []string{"GOTOOLCHAIN=local", "GOENV=off"}, "go", "env", "GOROOT")
	if err != nil {
		return "", err
	}
	license, err := readToolchainNotice(strings.TrimSpace(string(goRoot)), "LICENSE")
	if err != nil {
		return "", err
	}
	files["licenses/Go-LICENSE.txt"] = license
	notice, err := readToolchainNotice(strings.TrimSpace(string(goRoot)), "PATENTS")
	if err != nil {
		return "", err
	}
	files["licenses/Go-PATENTS.txt"] = notice
	target := strings.Split(c.Target, "-")
	env := []string{"CGO_ENABLED=0", "GOOS=" + target[0], "GOARCH=" + target[1], "GOAMD64=v1", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOEXPERIMENT=", "GOFIPS140=off"}
	for _, name := range Commands {
		binary := name
		if target[0] == "windows" {
			binary += ".exe"
		}
		p := filepath.Join(stage, binary)
		if _, err := Command(ctx, buildRoot, env, "go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-buildid=", "-o", p, "./cmd/"+name); err != nil {
			return "", err
		}
		b, err := ReadRegular(stage, binary)
		if err != nil {
			return "", err
		}
		files["bin/"+binary] = b
	}
	for n, b := range files {
		manifest.Files = append(manifest.Files, Fingerprint(n, b, fileMode(n)))
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	files["manifest.json"] = append(b, '\n')
	archive := filepath.Join(stage, "bundle.zip")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	err = WriteZIP(f, files)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return "", err
	}
	f, err = os.Open(archive)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	err = errors.Join(err, f.Close())
	if err != nil {
		return "", err
	}
	sum = hex.EncodeToString(hash.Sum(nil))
	checksum := filepath.Join(stage, "checksum")
	if err := os.WriteFile(checksum, []byte(sum+"  "+filepath.Base(output)+"\n"), 0644); err != nil {
		return "", err
	}
	if err := Publish(archive, checksum, output, os.Link); err != nil {
		return "", err
	}
	return sum, nil
}

const projectLicense = `ChronoLens project license: not selected.
This unsigned local-preview artifact is for owner-authorized local evaluation.
It is not a public redistribution grant or a production-ready release.
Dependency notices in licenses/ apply to their respective third-party software;
they do not select a license for the ChronoLens project.
`

const runInstructions = `ChronoLens LOCAL PREVIEW (unsigned; no installer)

Extract the ZIP completely to a new directory, then cd to that extracted root.
Windows PowerShell:
  .\bin\generator.exe -events 10000 -output events.jsonl
  .\bin\pack.exe -input events.jsonl -output events.clens
  .\bin\query.exe -input events.clens -format snapshot -engine indexed
  .\bin\bench.exe -input events.clens -format snapshot -samples 1 -window 1ms -max-iterations 3
  .\bin\server.exe -input events.clens -format snapshot -web web\dist -listen 127.0.0.1:8080
Linux amd64:
  chmod +x bin/*  # only needed if your ZIP extractor discarded Unix permissions
  ./bin/generator -events 10000 -output events.jsonl
  ./bin/pack -input events.jsonl -output events.clens
  ./bin/query -input events.clens -format snapshot -engine indexed
  ./bin/bench -input events.clens -format snapshot -samples 1 -window 1ms -max-iterations 3
  ./bin/server -input events.clens -format snapshot -web web/dist -listen 127.0.0.1:8080

Open http://127.0.0.1:8080 in a browser. Keep the server terminal open.
In a second terminal at the extracted root, exercise the load tool:
  Windows: .\bin\load.exe -duration 1s -rate 2 -requests 2
  Linux:   ./bin/load -duration 1s -rate 2 -requests 2
Stop your server with Ctrl+C. Every executable accepts -help.
No Go, Node, npm, source tree, or Internet is needed to run this bundle.
Use only trusted local datasets. There is NO AUTHENTICATION. Bind only to numeric
loopback; do not proxy/tunnel/expose it publicly. This is not production-ready.
Windows SmartScreen/antivirus may warn about unsigned binaries: verify provenance
and SHA256; do not disable system-wide protections. No installer or signing exists.
The project license is unselected; see PROJECT-LICENSE.txt and licenses/.
The archive SHA256 sidecar verifies transfer integrity, not publisher identity.
manifest.json records file hashes (excluding itself), source fingerprints and tools.
`
