package benchmark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

const fixture = `{"timestamp_us":0,"service":"z","duration_us":10,"status":200}
{"timestamp_us":499,"service":"api","duration_us":20,"status":500}
{"timestamp_us":499,"service":"z","duration_us":0,"status":503}
{"timestamp_us":500,"service":"api","duration_us":40,"status":404}
{"timestamp_us":999,"service":"z","duration_us":30,"status":200}
`

func pointer[T any](value T) *T { return &value }

func fixtureFile(t *testing.T, content string) string {
	t.Helper()
	file, err := os.CreateTemp(".", ".bench-fixture-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(file.Name()); err != nil {
			t.Error(err)
		}
	})
	_, writeErr := io.WriteString(file, content)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}

func quickConfig(path string) Config {
	config := DefaultConfig()
	config.Input, config.Samples = path, 1
	config.Window, config.MaxIterations = time.Nanosecond, 3
	return config
}

func TestRunKnownAnswersAndReport(t *testing.T) {
	path := fixtureFile(t, fixture)
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeLimit := debug.SetMemoryLimit(-1)
	report, err := Run(context.Background(), quickConfig(absolute))
	if err != nil {
		t.Fatal(err)
	}
	expected := []query.Aggregate{
		{Count: 2, ErrorCount: 2, DurationSumUS: 20, MeanDurationUS: pointer(10.0), MinDurationUS: pointer(uint32(0)), MaxDurationUS: pointer(uint32(20))},
		{Count: 3, ErrorCount: 2, DurationSumUS: 60, MeanDurationUS: pointer(20.0), MinDurationUS: pointer(uint32(0)), MaxDurationUS: pointer(uint32(40))},
		{Count: 3, ErrorCount: 2, DurationSumUS: 60, MeanDurationUS: pointer(20.0), MinDurationUS: pointer(uint32(0)), MaxDurationUS: pointer(uint32(40))},
		{Count: 5, ErrorCount: 2, DurationSumUS: 100, MeanDurationUS: pointer(20.0), MinDurationUS: pointer(uint32(0)), MaxDurationUS: pointer(uint32(40))},
		{Count: 2, ErrorCount: 1, DurationSumUS: 60, MeanDurationUS: pointer(30.0), MinDurationUS: pointer(uint32(20)), MaxDurationUS: pointer(uint32(40))},
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(fixture)))
	if report.Input != filepath.Base(path) || report.Source != (Source{
		InputFormat: query.JSONLFormat, SHA256: wantHash, Bytes: int64(len(fixture)), Events: 5, Services: 2,
		MinUS: 0, MaxUS: 999, TimeSpanUS: 1000,
	}) || report.SchemaVersion != 1 {
		t.Fatalf("incorrect source: %+v", report)
	}
	if report.Runtime.GoVersion != runtime.Version() || report.Runtime.OS != runtime.GOOS ||
		report.Runtime.Arch != runtime.GOARCH || report.Runtime.GOMAXPROCS != runtime.GOMAXPROCS(0) ||
		report.Runtime.MemoryLimitBytes != beforeLimit || debug.SetMemoryLimit(-1) != beforeLimit {
		t.Fatalf("incorrect or modified runtime: %+v", report.Runtime)
	}
	if len(report.Engines) != 3 || len(report.Cases) != 5 || len(report.Notes) == 0 {
		t.Fatal("missing report dimensions or measurement notes")
	}
	for engineIndex, engine := range report.Engines {
		if engine.Engine != []query.Engine{query.Row, query.Columnar, query.Indexed}[engineIndex] ||
			engine.Load.SHA256 != wantHash || engine.Load.Bytes != int64(len(fixture)) ||
			engine.Heap.DeltaBytes != int64(engine.Heap.AfterBytes)-int64(engine.Heap.BeforeBytes) {
			t.Fatalf("incorrect engine metadata: %+v", engine)
		}
		for i, result := range engine.Cases {
			if !reflect.DeepEqual(result.Result.Aggregate, expected[i]) {
				t.Fatalf("%s/%s: %+v, want %+v", engine.Engine, result.Name, result.Result.Aggregate, expected[i])
			}
			wantCandidates := 5
			if engine.Engine == query.Indexed && i < 4 {
				wantCandidates = int(expected[i].Count)
			}
			if result.Result.Stats.RowsExamined != wantCandidates ||
				result.Result.Stats.RowsSkipped != 5-wantCandidates || result.Result.Stats.TotalRows != 5 {
				t.Fatalf("incorrect candidate statistics: %+v", result.Result.Stats)
			}
			if i < 4 && report.Cases[i].TimeMatchedRows != expected[i].Count {
				t.Fatal("time selectivity must report actual matches")
			}
			if len(result.Samples) != 1 || result.Samples[0].Iterations != 3 {
				t.Fatal("missing raw sample or minimum iterations")
			}
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "input", "settings", "runtime", "source", "cases", "engines", "notes"} {
		if _, ok := shape[key]; !ok {
			t.Fatalf("missing JSON key %s", key)
		}
	}
	if bytes.Contains(encoded, []byte("p95")) {
		t.Fatal("report exposes unsupported percentile")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != fixture {
		t.Fatal("benchmark modified the input")
	}
}

func TestDefaultConfigAndValidation(t *testing.T) {
	config := DefaultConfig()
	if err := config.Validate(); err != nil || config.MaxEvents != 1_000_000 ||
		config.Window < 100*time.Millisecond || config.Samples < 3 {
		t.Fatalf("unsafe defaults: %+v, %v", config, err)
	}
	config.MaxEvents = 0
	if report, err := Run(context.Background(), config); err == nil || !reflect.DeepEqual(report, Report{}) {
		t.Fatal("library must validate configuration before loading")
	}
}

func TestExactCenteredRanges(t *testing.T) {
	for _, bounds := range [][2]int64{{0, 999}, {10, 10}, {5, 6}, {0, 0}, {math.MaxInt64, math.MaxInt64}, {math.MaxInt64 - 1, math.MaxInt64}, {0, math.MaxInt64}} {
		t.Run(fmt.Sprint(bounds), func(t *testing.T) {
			cases, err := makeCases(query.Metadata{Rows: 2, Services: []string{"a"}, MinUS: &bounds[0], MaxUS: &bounds[1]})
			if err != nil {
				t.Fatal(err)
			}
			span := new(big.Int).Sub(big.NewInt(bounds[1]), big.NewInt(bounds[0]))
			span.Add(span, big.NewInt(1))
			for _, test := range cases[:4] {
				divisor := new(big.Int).SetUint64(test.SpanDivisor)
				width := new(big.Int).Add(span, new(big.Int).Sub(divisor, big.NewInt(1)))
				width.Div(width, divisor)
				offset := new(big.Int).Sub(span, width)
				offset.Div(offset, big.NewInt(2))
				from := new(big.Int).Add(big.NewInt(bounds[0]), offset)
				to := new(big.Int).Add(from, width)
				if test.Filter.FromUS != from.Int64() || test.ActualWidthUS != width.Uint64() {
					t.Fatalf("incorrect centered range: %+v", test)
				}
				if to.Cmp(big.NewInt(math.MaxInt64)) > 0 {
					if test.Filter.ToUS != nil {
						t.Fatal("MaxInt64+1 must be unbounded")
					}
				} else if test.Filter.ToUS == nil || *test.Filter.ToUS != to.Int64() {
					t.Fatal("incorrect exclusive endpoint")
				}
				if err := test.Filter.Validate(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMaxTimestampTiesAllEngines(t *testing.T) {
	content := strings.ReplaceAll(fixture, `"timestamp_us":0,`, `"timestamp_us":9223372036854775807,`)
	for _, value := range []string{"499", "500", "999"} {
		content = strings.ReplaceAll(content, `"timestamp_us":`+value+`,`, `"timestamp_us":9223372036854775807,`)
	}
	report, err := Run(context.Background(), quickConfig(fixtureFile(t, content)))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range report.Cases[:4] {
		if test.Filter.FromUS != math.MaxInt64 || test.Filter.ToUS != nil || test.TimeMatchedRows != 5 || test.ActualWidthUS != 1 {
			t.Fatalf("ties or max timestamp lost: %+v", test)
		}
	}
	for _, engine := range report.Engines {
		for _, test := range engine.Cases[:4] {
			if test.Result.Aggregate.Count != 5 {
				t.Fatal("maximum timestamp excluded")
			}
		}
	}
}

func TestInputFailures(t *testing.T) {
	for name, content := range map[string]string{
		"empty": "", "malformed": fixture + "broken\n", "blank": "\n",
		"unordered":     fixture + `{"timestamp_us":0,"service":"a","duration_us":1,"status":200}`,
		"unknown field": `{"timestamp_us":0,"service":"a","duration_us":1,"status":200,"extra":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			report, err := Run(context.Background(), quickConfig(fixtureFile(t, content)))
			if err == nil || !reflect.DeepEqual(report, Report{}) {
				t.Fatalf("input failure returned partial success: %+v, %v", report, err)
			}
		})
	}
	config := quickConfig(fixtureFile(t, fixture))
	config.MaxEvents = 4
	if _, err := Run(context.Background(), config); err == nil || !strings.Contains(err.Error(), "max-events") {
		t.Fatalf("expected event limit, got %v", err)
	}
	config.Input = "."
	if _, err := Run(context.Background(), config); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("expected regular-file rejection, got %v", err)
	}
}

func TestMutationRejected(t *testing.T) {
	path := fixtureFile(t, fixture)
	loads := 0
	changed := strings.Replace(fixture, `"duration_us":10`, `"duration_us":11`, 1)
	load := func(ctx context.Context, path string, engine query.Engine, maxEvents int, format query.InputFormat) (*query.Dataset, Load, error) {
		data, result, err := loadFile(ctx, path, engine, maxEvents, format)
		loads++
		if loads == 1 {
			if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return data, result, err
	}
	report, err := run(context.Background(), quickConfig(path), load)
	if err == nil || !strings.Contains(err.Error(), "SHA256") || loads != 2 || !reflect.DeepEqual(report, Report{}) {
		t.Fatalf("mutation silently accepted: %+v, %v, loads=%d", report, err, loads)
	}
}

func TestHashActualBytes(t *testing.T) {
	for _, content := range []string{fixture, strings.TrimSuffix(fixture, "\n"), strings.ReplaceAll(fixture, "\n", "\r\n")} {
		path := fixtureFile(t, content)
		for _, engine := range []query.Engine{query.Row, query.Columnar, query.Indexed} {
			data, result, err := loadFile(context.Background(), path, engine, 5, query.JSONLFormat)
			if err != nil || result.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(content))) ||
				result.Bytes != int64(len(content)) || data.Len() != 5 {
				t.Fatalf("incorrect actual-byte hash: %+v, %v", result, err)
			}
		}
	}
}

func TestDatasetMetadataAllLayouts(t *testing.T) {
	for _, engine := range []query.Engine{query.Row, query.Columnar, query.Indexed} {
		data, err := query.Load(context.Background(), strings.NewReader(fixture), engine, 5)
		if err != nil {
			t.Fatal(err)
		}
		want := query.Metadata{Rows: 5, Services: []string{"api", "z"}, Statuses: []uint16{200, 404, 500, 503}, MinUS: pointer(int64(0)), MaxUS: pointer(int64(999))}
		got := data.Metadata()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s metadata: %+v", engine, got)
		}
		got.Services[0], got.Statuses[0], *got.MinUS, *got.MaxUS = "mutated", 599, 1, 2
		if !reflect.DeepEqual(data.Metadata(), want) {
			t.Fatal("metadata aliases dataset storage")
		}
		empty, err := query.Load(context.Background(), strings.NewReader(""), engine, 5)
		if err != nil || empty.Metadata().MinUS != nil || empty.Metadata().MaxUS != nil || empty.Metadata().Rows != 0 {
			t.Fatal("incorrect empty metadata")
		}
	}
}

type cancellingReader struct{ cancel context.CancelFunc }

func (r cancellingReader) Read(p []byte) (int, error) {
	r.cancel()
	return copy(p, fixture), nil
}

func TestCancellationAndDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, quickConfig("does-not-exist")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation before open, got %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	reader := &contextReader{ctx: ctx, input: cancellingReader{cancel: cancel}}
	if _, err := query.Load(ctx, reader, query.Row, 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation during read, got %v", err)
	}
	config := quickConfig("does-not-exist")
	config.Timeout = time.Nanosecond
	_, err := run(context.Background(), config, func(ctx context.Context, _ string, _ query.Engine, _ int, _ query.InputFormat) (*query.Dataset, Load, error) {
		<-ctx.Done()
		return nil, Load{}, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected global deadline, got %v", err)
	}
}

func TestRunRejectsCrossEngineAggregateMismatch(t *testing.T) {
	loads := 0
	load := func(ctx context.Context, _ string, engine query.Engine, maxEvents int, _ query.InputFormat) (*query.Dataset, Load, error) {
		content := fixture
		loads++
		if loads == 2 {
			content = strings.Replace(content, `"duration_us":20`, `"duration_us":21`, 1)
		}
		data, err := query.Load(ctx, strings.NewReader(content), engine, maxEvents)
		// Deliberately bypass the independent hash guard to test aggregate checks.
		return data, Load{SHA256: "same", Bytes: int64(len(content))}, err
	}
	if _, err := run(context.Background(), quickConfig("unused"), load); err == nil ||
		!strings.Contains(err.Error(), "aggregate differs") {
		t.Fatalf("count-only equivalence would miss duration changes: %v", err)
	}
}
