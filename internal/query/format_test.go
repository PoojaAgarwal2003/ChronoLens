package query

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/generator"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/snapshot"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

func snapshotInput(t testing.TB, input []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	if _, err := snapshot.WriteJSONL(context.Background(), &output, bytes.NewReader(input), 1000000); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestInputFormatParsingAndValidationBeforeRead(t *testing.T) {
	for _, format := range []InputFormat{JSONLFormat, SnapshotFormat} {
		if got, err := ParseInputFormat(string(format)); err != nil || got != format {
			t.Fatalf("parse %q: %q, %v", format, got, err)
		}
		for _, test := range []struct {
			engine Engine
			limit  int
		}{{"unknown", 1}, {Row, 0}, {Columnar, -1}} {
			reader := &unreadableInput{t: t}
			if data, err := LoadFormat(context.Background(), reader, test.engine, test.limit, format); data != nil || err == nil {
				t.Fatalf("invalid options returned %v, %v", data, err)
			}
			if test.limit <= 0 {
				if catalog, err := LoadCatalogFormat(context.Background(), reader, test.limit, format); catalog != nil || err == nil {
					t.Fatalf("invalid limit returned %v, %v", catalog, err)
				}
			}
		}
	}
	for _, value := range []string{"", "auto", "JSONL", "Snapshot", " jsonl", "snapshot ", "csv"} {
		if got, err := ParseInputFormat(value); got != "" || err == nil {
			t.Fatalf("invalid format %q returned %q, %v", value, got, err)
		}
		reader := &unreadableInput{t: t}
		if data, err := LoadFormat(context.Background(), reader, Row, 1, InputFormat(value)); data != nil || err == nil {
			t.Fatalf("invalid format returned %v, %v", data, err)
		}
		if catalog, err := LoadCatalogFormat(context.Background(), reader, 1, InputFormat(value)); catalog != nil || err == nil {
			t.Fatalf("invalid format returned %v, %v", catalog, err)
		}
	}
}

type unreadableInput struct{ t *testing.T }

func (r *unreadableInput) Read([]byte) (int, error) {
	r.t.Error("invalid options or cancelled context performed input I/O")
	return 0, io.ErrUnexpectedEOF
}

func TestFormatsPreserveLayoutsResultsProfilesAndMetadata(t *testing.T) {
	inputs := map[string][]byte{
		"empty": nil,
		"ties":  []byte(fixture),
		"boundaries": []byte(`{"timestamp_us":0,"service":"\u670d\u52a1\ud83d\ude80","duration_us":0,"status":100}
{"timestamp_us":0,"service":"a","duration_us":4294967295,"status":599}
{"timestamp_us":9223372036854775807,"service":"a","duration_us":4294967295,"status":500}
{"timestamp_us":9223372036854775807,"service":"\u670d\u52a1\ud83d\ude80","duration_us":1,"status":200}
`),
	}
	for _, profile := range []string{generator.Uniform, generator.Incident} {
		var output bytes.Buffer
		if err := generator.Generate(context.Background(), &output, generator.Config{
			Events: 8209, Services: 32, Seed: 42, Start: time.Unix(0, 0),
			Interval: 4 * time.Microsecond, ErrorPercent: 15, Profile: profile,
		}); err != nil {
			t.Fatal(err)
		}
		inputs[profile] = output.Bytes()
	}
	filters := []Filter{
		{}, {FromUS: 2, ToUS: pointer(int64(3))}, {ToUS: pointer(int64(2))},
		{FromUS: 2, ToUS: pointer(int64(2))}, {FromUS: math.MaxInt64},
		{ToUS: pointer(int64(math.MaxInt64))}, {Service: "missing"},
		{Service: "\u670d\u52a1\U0001f680"}, {Status: 100}, {Status: 599},
		{FromUS: 3000, ToUS: pointer(int64(5000)), Service: "service-001", Status: 500},
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			encoded := snapshotInput(t, input)
			reference, err := LoadCatalog(context.Background(), bytes.NewReader(input), 10000)
			if err != nil || reference.InputFormat() != JSONLFormat {
				t.Fatalf("JSONL compatibility catalog: %v, %v", reference, err)
			}
			baseline, _ := reference.Engine(Row)
			for _, format := range []InputFormat{JSONLFormat, SnapshotFormat} {
				source := input
				if format == SnapshotFormat {
					source = encoded
				}
				catalog, err := LoadCatalogFormat(context.Background(), bytes.NewReader(source), 10000, format)
				if err != nil {
					t.Fatal(err)
				}
				if catalog.InputFormat() != format || !reflect.DeepEqual(catalog.Metadata(), reference.Metadata()) {
					t.Fatalf("%s catalog metadata differs", format)
				}
				columnar, _ := catalog.Engine(Columnar)
				indexed, _ := catalog.Engine(Indexed)
				if columnar.Len() > 0 && (&columnar.timestamps[0] != &indexed.timestamps[0] ||
					&columnar.serviceIDs[0] != &indexed.serviceIDs[0] ||
					&columnar.durations[0] != &indexed.durations[0] || &columnar.statuses[0] != &indexed.statuses[0]) {
					t.Fatal("catalog duplicated indexed columns")
				}
				for _, engine := range []Engine{Row, Columnar, Indexed} {
					data, err := LoadFormat(context.Background(), bytes.NewReader(source), engine, 10000, format)
					if err != nil {
						t.Fatal(err)
					}
					want, err := Load(context.Background(), bytes.NewReader(input), engine, 10000)
					if err != nil {
						t.Fatal(err)
					}
					wantCatalog, _ := reference.Engine(engine)
					fromCatalog, _ := catalog.Engine(engine)
					if !reflect.DeepEqual(data, want) || !reflect.DeepEqual(fromCatalog, wantCatalog) ||
						!reflect.DeepEqual(data.Metadata(), reference.Metadata()) {
						t.Fatalf("%s/%s layout, dictionary, or metadata differs", format, engine)
					}
					for _, filter := range filters {
						expected, err := want.Query(context.Background(), filter)
						if err != nil {
							t.Fatal(err)
						}
						got, err := data.Query(context.Background(), filter)
						aggregate, _ := baseline.Query(context.Background(), filter)
						if err != nil || !reflect.DeepEqual(got, expected) || !reflect.DeepEqual(got.Aggregate, aggregate.Aggregate) {
							t.Fatalf("%s/%s query %+v differs: %+v / %+v (%v)", format, engine, filter, got, expected, err)
						}
						for _, buckets := range []int{1, 17, MaxBuckets} {
							got, gotErr := data.Profile(context.Background(), filter, buckets)
							expected, wantErr := want.Profile(context.Background(), filter, buckets)
							if (gotErr != nil) != (wantErr != nil) || !reflect.DeepEqual(got, expected) {
								t.Fatalf("%s/%s profile differs: %v / %v", format, engine, gotErr, wantErr)
							}
							if engine == Row && gotErr == nil {
								t.Fatal("format loading changed row profile support")
							}
						}
					}
				}
			}
		})
	}
}

func requireRejectedFormat(t *testing.T, input []byte, limit int, format InputFormat) {
	t.Helper()
	for _, engine := range []Engine{Row, Columnar, Indexed} {
		if data, err := LoadFormat(context.Background(), bytes.NewReader(input), engine, limit, format); data != nil || err == nil {
			t.Fatalf("%s exposed failed %s input: %v, %v", engine, format, data, err)
		}
	}
	if catalog, err := LoadCatalogFormat(context.Background(), bytes.NewReader(input), limit, format); catalog != nil || err == nil {
		t.Fatalf("catalog exposed failed %s input: %v, %v", format, catalog, err)
	}
}

func TestFormatRejectsCorruptionTruncationAndWrongFormat(t *testing.T) {
	encoded := snapshotInput(t, []byte(fixture))
	requireRejectedFormat(t, encoded, 10, JSONLFormat)
	requireRejectedFormat(t, []byte(fixture), 10, SnapshotFormat)
	requireRejectedFormat(t, []byte(fixture+"broken"), 10, JSONLFormat)
	for end := 0; end < len(encoded); end++ {
		t.Run(fmt.Sprintf("truncated-at-%d", end), func(t *testing.T) {
			requireRejectedFormat(t, encoded[:end], 10, SnapshotFormat)
		})
	}
	for name, offset := range map[string]int{
		"header": 0, "payload": 34, "crc": len(encoded) - 53,
		"trailer-marker": len(encoded) - 52, "totals": len(encoded) - 48, "sha": len(encoded) - 1,
	} {
		t.Run(name, func(t *testing.T) {
			corrupt := bytes.Clone(encoded)
			corrupt[offset] ^= 1
			requireRejectedFormat(t, corrupt, 10, SnapshotFormat)
		})
	}
	requireRejectedFormat(t, append(bytes.Clone(encoded), 0), 10, SnapshotFormat)
	multi := snapshotInput(t, generatedInput(t, 4100))
	second := 16 + 16 + int(binary.LittleEndian.Uint32(multi[28:])) + 4
	multi[second+16] ^= 1
	requireRejectedFormat(t, multi, 4100, SnapshotFormat)
}

func TestFormatsEnforceEventLimitsAndCancellation(t *testing.T) {
	for _, format := range []InputFormat{JSONLFormat, SnapshotFormat} {
		input := []byte(fixture)
		if format == SnapshotFormat {
			input = snapshotInput(t, input)
		}
		requireRejectedFormat(t, input, 3, format)
		for _, engine := range []Engine{Row, Columnar, Indexed} {
			if data, err := LoadFormat(context.Background(), bytes.NewReader(input), engine, 4, format); err != nil || data.Len() != 4 {
				t.Fatalf("exact event limit failed: %v", err)
			}
			for _, duringRead := range []bool{false, true} {
				ctx, cancel := context.WithCancel(context.Background())
				var reader io.Reader = &unreadableInput{t: t}
				var loadCtx context.Context = ctx
				if duringRead {
					reader = bytes.NewReader(input)
					loadCtx = &cancelOnCheck{Context: ctx, cancel: cancel}
				} else {
					cancel()
				}
				data, err := LoadFormat(loadCtx, reader, engine, 4, format)
				cancel()
				if data != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled load returned %v, %v", data, err)
				}
			}
		}
		if catalog, err := LoadCatalogFormat(context.Background(), bytes.NewReader(input), 4, format); err != nil || catalog.Metadata().Rows != 4 {
			t.Fatalf("exact catalog limit failed: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		catalog, err := LoadCatalogFormat(&cancelOnCheck{Context: ctx, cancel: cancel}, bytes.NewReader(input), 4, format)
		cancel()
		if catalog != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled catalog returned %v, %v", catalog, err)
		}
	}
}

func TestSharedAppendCallbackLimits(t *testing.T) {
	for _, engine := range []Engine{Row, Columnar, Indexed} {
		data := &Dataset{engine: engine, dictionary: make(map[string]uint16)}
		info, err := snapshot.Read(context.Background(), bytes.NewReader(snapshotInput(t, []byte(fixture))), 4,
			func(event telemetry.Event) error { return data.appendEvent(event, 3) })
		if err == nil || info != (snapshot.Info{}) || data.Len() != 3 || !strings.Contains(err.Error(), "callback") {
			t.Fatalf("callback limit was not propagated: %+v, %v", info, err)
		}
	}
}

func TestSnapshotCatalogConcurrentImmutableQueries(t *testing.T) {
	catalog, err := LoadCatalogFormat(context.Background(), bytes.NewReader(snapshotInput(t, []byte(fixture))), 4, SnapshotFormat)
	if err != nil {
		t.Fatal(err)
	}
	wantMeta := catalog.Metadata()
	var workers sync.WaitGroup
	for _, engine := range []Engine{Row, Columnar, Indexed} {
		data, _ := catalog.Engine(engine)
		want, _ := data.Query(context.Background(), Filter{})
		for range 4 {
			workers.Go(func() {
				for range 20 {
					got, err := data.Query(context.Background(), Filter{})
					if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(catalog.Metadata(), wantMeta) {
						t.Errorf("concurrent %s query mutated results: %v", engine, err)
						return
					}
					if engine != Row {
						profile, err := data.Profile(context.Background(), Filter{}, 3)
						if err != nil || len(profile.Services) != 2 {
							t.Errorf("concurrent profile failed: %+v, %v", profile, err)
							return
						}
						profile.Services[0].Service = "changed"
					}
					meta := catalog.Metadata()
					meta.Services[0], meta.Statuses[0], *meta.MinUS, *meta.MaxUS = "changed", 599, 0, 0
				}
			})
		}
	}
	workers.Wait()
}
