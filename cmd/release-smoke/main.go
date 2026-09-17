// release-smoke is developer/CI-only verification, not an end-user dependency.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/release"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("release-smoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	archive := flags.String("archive", "", "preview ZIP with adjacent .sha256")
	workRoot := flags.String("work-root", "", "existing clean-workspace parent OUTSIDE source repository")
	browser := flags.String("browser-script", "", "optional absolute path to web/release-browser-smoke.mjs (developer Node/Playwright required)")
	verifyOnly := flags.Bool("verify-only", false, "verify either target and emit JSON manifest; never extract or execute")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *archive == "" ||
		!*verifyOnly && *workRoot == "" || *verifyOnly && (*workRoot != "" || *browser != "") {
		fmt.Fprintln(stderr, "release-smoke: require -archive and either -verify-only (no work-root/browser-script) or -work-root (outside repository)")
		return 2
	}
	if *verifyOnly {
		manifest, _, err := release.Verify(*archive)
		if err == nil {
			err = json.NewEncoder(stdout).Encode(struct {
				Verified  bool              `json:"verified"`
				Extracted bool              `json:"extracted"`
				Executed  bool              `json:"executed"`
				Manifest  *release.Manifest `json:"manifest"`
			}{Verified: true, Manifest: manifest})
		}
		if err != nil {
			fmt.Fprintln(stderr, "release-smoke:", err)
			return 1
		}
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := smoke(ctx, *archive, *workRoot, *browser); err != nil {
		fmt.Fprintln(stderr, "release-smoke:", err)
		return 1
	}
	fmt.Fprintln(stdout, "PASS: archive checksum + complete manifest; isolated extraction; all six CLIs; aggregate equality; HTTP/static assets; owned server stopped; workspace removed")
	return 0
}

func smoke(ctx context.Context, archive, workRoot, browser string) (resultErr error) {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	workRoot, err = filepath.Abs(workRoot)
	if err != nil {
		return err
	}
	realWork, err := filepath.EvalSymlinks(workRoot)
	if err != nil {
		return err
	}
	realCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(realCWD, realWork)
	if err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("work-root must be outside the current source working directory")
	}
	manifest, files, err := release.Verify(archive)
	if err != nil {
		return err
	}
	if manifest.Target != runtime.GOOS+"-"+runtime.GOARCH {
		return fmt.Errorf("cannot execute %s on %s-%s", manifest.Target, runtime.GOOS, runtime.GOARCH)
	}
	root, err := os.MkdirTemp(workRoot, "chronolens-isolated-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	dir := filepath.Join(root, "app")
	if err := release.Extract(dir, files); err != nil {
		return err
	}
	fmt.Println("Isolated workspace:", dir)
	executable := func(n string) string {
		if runtime.GOOS == "windows" {
			n += ".exe"
		}
		return filepath.Join(dir, "bin", n)
	}
	run := func(n string, args ...string) ([]byte, error) {
		child, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// Absolute executable + extracted-root cwd: no Go, Node, PATH lookup,
		// source path, or build operation participates in the application flows.
		return release.Command(child, dir, []string{"PATH="}, executable(n), args...)
	}
	for _, out := range []string{"events.jsonl", "repeat.jsonl"} {
		if _, err := run("generator", "-events", "1000", "-services", "4", "-seed", "42", "-output", out); err != nil {
			return err
		}
	}
	a, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, "repeat.jsonl"))
	if err != nil || !bytes.Equal(a, b) {
		return errors.New("generator fixture is not deterministic")
	}
	if _, err := run("pack", "-input", "events.jsonl", "-output", "events.clens"); err != nil {
		return err
	}
	var baseline any
	for _, format := range []string{"jsonl", "snapshot"} {
		input := "events.jsonl"
		if format == "snapshot" {
			input = "events.clens"
		}
		for _, engine := range []string{"row", "columnar", "indexed"} {
			out, err := run("query", "-input", input, "-format", format, "-engine", engine)
			if err != nil {
				return err
			}
			var report struct {
				Result struct{ Aggregate map[string]any }
			}
			if err := json.Unmarshal(out, &report); err != nil {
				return err
			}
			if report.Result.Aggregate["count"] != float64(1000) {
				return errors.New("unexpected query count")
			}
			if baseline == nil {
				baseline = report.Result.Aggregate
			} else if !reflect.DeepEqual(baseline, report.Result.Aggregate) {
				return errors.New("cross-format/engine aggregate mismatch")
			}
		}
	}
	out, err := run("bench", "-input", "events.clens", "-format", "snapshot", "-samples", "1",
		"-window", "1ms", "-max-iterations", "3", "-timeout", "15s")
	if err != nil {
		return err
	}
	var bench struct {
		Source struct {
			Events      int
			SHA256      string
			InputFormat string `json:"input_format"`
		}
		Engines []struct {
			Engine string
			Cases  []struct {
				Name   string
				Result struct{ Aggregate map[string]any }
			}
		}
	}
	if err := json.Unmarshal(out, &bench); err != nil || bench.Source.Events != 1000 ||
		bench.Source.InputFormat != "snapshot" || len(bench.Engines) != 3 {
		return fmt.Errorf("invalid bench report: %v", err)
	}
	snapshot, err := os.ReadFile(filepath.Join(dir, "events.clens"))
	if err != nil || bench.Source.SHA256 != release.Fingerprint("", snapshot, 0).SHA256 {
		return errors.New("benchmark source fingerprint mismatch")
	}
	engines := map[string]bool{}
	for _, engine := range bench.Engines {
		if engines[engine.Engine] || len(engine.Cases) == 0 || len(engine.Cases) != len(bench.Engines[0].Cases) {
			return errors.New("benchmark engine/case coverage mismatch")
		}
		engines[engine.Engine] = true
		for i, c := range engine.Cases {
			want := bench.Engines[0].Cases[i]
			if c.Name != want.Name || len(c.Result.Aggregate) == 0 || !reflect.DeepEqual(c.Result.Aggregate, want.Result.Aggregate) {
				return errors.New("benchmark cross-engine aggregate mismatch")
			}
		}
	}
	if !engines["row"] || !engines["columnar"] || !engines["indexed"] {
		return errors.New("benchmark omitted an engine")
	}
	server := exec.CommandContext(ctx, executable("server"), "-input", "events.clens", "-format", "snapshot",
		"-web", filepath.Join("web", "dist"), "-listen", "127.0.0.1:0")
	server.Dir = dir
	server.Env = append(os.Environ(), "PATH=")
	server.WaitDelay = 2 * time.Second
	stdout, err := server.StdoutPipe()
	if err != nil {
		return err
	}
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		return err
	}
	defer func() {
		_ = server.Process.Kill() // Only the child started by this invocation.
		_ = server.Wait()
	}()
	urls := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			urls <- strings.TrimPrefix(scanner.Text(), "ChronoLens listening on ")
		} else {
			urls <- ""
		}
	}()
	var base string
	select {
	case base = <-urls:
	case <-time.After(15 * time.Second):
		return errors.New("server startup timeout")
	case <-ctx.Done():
		return ctx.Err()
	}
	if !regexp.MustCompile(`^http://127\.0\.0\.1:[0-9]+$`).MatchString(base) {
		return fmt.Errorf("unexpected server address %q", base)
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	defer client.CloseIdleConnections()
	request := func(method, p, body string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, base+p, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		if method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("%s: HTTP %d: %s", p, resp.StatusCode, b)
		}
		return b, err
	}
	health, err := request("GET", "/api/health", "")
	if err != nil || !bytes.Contains(health, []byte(`"status":"ok"`)) {
		return fmt.Errorf("health failed: %s %v", health, err)
	}
	meta, err := request("GET", "/api/meta", "")
	var metadata struct {
		Rows        int
		InputFormat string `json:"input_format"`
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(meta, &metadata); err != nil || metadata.Rows != 1000 || metadata.InputFormat != "snapshot" {
		return errors.New("metadata did not describe packaged snapshot")
	}
	query, err := request("POST", "/api/query", `{"engine":"indexed","from_us":"0","to_us":null,"service":"","status":0,"buckets":10}`)
	if err != nil {
		return err
	}
	var qr struct {
		Result struct{ Aggregate struct{ Count int } }
	}
	if err := json.Unmarshal(query, &qr); err != nil || qr.Result.Aggregate.Count != 1000 {
		return errors.New("HTTP aggregate mismatch")
	}
	index, err := request("GET", "/", "")
	if err != nil || !bytes.Equal(index, files["web/dist/index.html"]) {
		return errors.New("static index did not match packaged bytes")
	}
	matches := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllSubmatch(index, -1)
	if len(matches) == 0 {
		return errors.New("index contains no built asset paths")
	}
	for _, match := range matches {
		p := string(match[1])
		asset, err := request("GET", p, "")
		if err != nil || !bytes.Equal(asset, files["web/dist"+p]) {
			return fmt.Errorf("static asset mismatch: %s: %v", p, err)
		}
	}
	out, err = run("load", "-target", base+"/api/query", "-duration", "1s", "-rate", "2", "-requests", "2", "-timeout", "2s")
	if err != nil {
		return err
	}
	var load struct {
		Scheduled int            `json:"scheduled"`
		Sent      int            `json:"sent"`
		Skipped   int            `json:"skipped"`
		Statuses  map[string]int `json:"http_statuses"`
		RunError  string         `json:"run_error"`
	}
	if err := json.Unmarshal(out, &load); err != nil || load.Scheduled != 2 ||
		load.Sent != 2 || load.Skipped != 0 || load.Statuses["200"] != 2 || load.RunError != "" {
		return fmt.Errorf("load smoke did not complete both HTTP requests successfully: %s", out)
	}
	// Keep raw validation output available without retaining datasets/workspaces.
	fmt.Println("Load report:", string(out))
	if browser != "" {
		browser, err = filepath.Abs(browser)
		if err != nil {
			return err
		}
		if _, err := release.Command(ctx, dir, nil, "node", browser, base); err != nil {
			return err
		}
		fmt.Println("PASS: packaged UI browser smoke")
	}
	return nil
}
