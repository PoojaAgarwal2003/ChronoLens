package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/generator"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

const fixture = `{"timestamp_us":1,"service":"a","duration_us":10,"status":200}
{"timestamp_us":2,"service":"b","duration_us":20,"status":500}
{"timestamp_us":2,"service":"a","duration_us":0,"status":503}
{"timestamp_us":3,"service":"a","duration_us":40,"status":404}
`

func pointer[T any](value T) *T { return &value }

func loadFixture(t testing.TB, input string, engine Engine) *Dataset {
	t.Helper()
	data, err := Load(context.Background(), strings.NewReader(input), engine, 1000000)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestQueryKnownResults(t *testing.T) {
	tests := []struct {
		name   string
		filter Filter
		want   Aggregate
	}{
		{"all", Filter{}, Aggregate{4, 2, 70, pointer(17.5), pointer(uint32(0)), pointer(uint32(40))}},
		{"half-open range", Filter{FromUS: 2, ToUS: pointer(int64(3))}, Aggregate{2, 2, 20, pointer(10.0), pointer(uint32(0)), pointer(uint32(20))}},
		{"service", Filter{Service: "a"}, Aggregate{3, 1, 50, pointer(50.0 / 3), pointer(uint32(0)), pointer(uint32(40))}},
		{"client errors are not server errors", Filter{Status: 404}, Aggregate{1, 0, 40, pointer(40.0), pointer(uint32(40)), pointer(uint32(40))}},
		{"combined", Filter{FromUS: 2, ToUS: pointer(int64(3)), Service: "a", Status: 503}, Aggregate{1, 1, 0, pointer(0.0), pointer(uint32(0)), pointer(uint32(0))}},
		{"unknown service", Filter{Service: "missing"}, Aggregate{}},
		{"absent status", Filter{Status: 201}, Aggregate{}},
		{"empty range", Filter{FromUS: 2, ToUS: pointer(int64(2))}, Aggregate{}},
		{"outside data", Filter{FromUS: 100}, Aggregate{}},
	}
	for _, engine := range []Engine{Row, Columnar} {
		data := loadFixture(t, fixture, engine)
		if data.Len() != 4 || data.ServiceCount() != 2 {
			t.Fatal("incorrect dataset metadata")
		}
		if (engine == Row && len(data.timestamps) != 0) || (engine == Columnar && len(data.rows) != 0) {
			t.Fatal("dataset retained both storage layouts")
		}
		for _, test := range tests {
			t.Run(string(engine)+"/"+test.name, func(t *testing.T) {
				got, err := data.Query(context.Background(), test.filter)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Aggregate, test.want) {
					gotJSON, _ := json.Marshal(got.Aggregate)
					wantJSON, _ := json.Marshal(test.want)
					t.Fatalf("got %s, want %s", gotJSON, wantJSON)
				}
				if got.Stats != (Stats{Engine: engine, TotalRows: 4, RowsExamined: 4}) {
					t.Fatalf("incorrect full-scan stats: %+v", got.Stats)
				}
			})
		}
	}
}

func TestQueryEmptyAndIntegerBoundaries(t *testing.T) {
	input := `{"timestamp_us":9223372036854775807,"service":"a","duration_us":4294967295,"status":599}`
	for _, engine := range []Engine{Row, Columnar} {
		empty := loadFixture(t, "", engine)
		got, err := empty.Query(context.Background(), Filter{})
		if err != nil || !reflect.DeepEqual(got.Aggregate, Aggregate{}) || got.Stats.RowsExamined != 0 {
			t.Fatalf("incorrect empty result: %+v, %v", got, err)
		}
		data := loadFixture(t, input, engine)
		got, err = data.Query(context.Background(), Filter{FromUS: math.MaxInt64})
		if err != nil || got.Aggregate.Count != 1 || got.Aggregate.DurationSumUS != math.MaxUint32 {
			t.Fatalf("integer boundary lost: %+v, %v", got, err)
		}
		excluded, err := data.Query(context.Background(), Filter{ToUS: pointer(int64(math.MaxInt64))})
		if err != nil || excluded.Aggregate.Count != 0 {
			t.Fatal("exclusive upper bound included its endpoint")
		}
	}
}

func TestQueryRejectsInvalidFilterAndCancellation(t *testing.T) {
	filters := []Filter{
		{FromUS: -1}, {ToUS: pointer(int64(-1))}, {FromUS: 3, ToUS: pointer(int64(2))},
		{Status: 99}, {Status: 600}, {Service: " a"}, {Service: strings.Repeat("x", 129)},
	}
	for _, engine := range []Engine{Row, Columnar} {
		data := loadFixture(t, fixture, engine)
		for _, filter := range filters {
			result, err := data.Query(context.Background(), filter)
			if err == nil || !reflect.DeepEqual(result, Result{}) {
				t.Fatalf("invalid filter returned partial success: %+v, %v", result, err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := data.Query(ctx, Filter{})
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, Result{}) {
			t.Fatalf("expected cancellation without partial result, got %+v, %v", result, err)
		}
	}
	var uninitialized Dataset
	if _, err := uninitialized.Query(context.Background(), Filter{}); err == nil {
		t.Fatal("uninitialized dataset succeeded")
	}
}

type cancelOnCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelOnCheck) Err() error {
	c.checks++
	if c.checks == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestQueryCancellationDiscardsAccumulatedResults(t *testing.T) {
	for _, engine := range []Engine{Row, Columnar} {
		for _, input := range []string{fixture, string(generatedInput(t, 2000))} {
			data := loadFixture(t, input, engine)
			ctx, cancel := context.WithCancel(context.Background())
			controlled := &cancelOnCheck{Context: ctx, cancel: cancel}
			result, err := data.Query(controlled, Filter{})
			cancel()
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, Result{}) {
				t.Fatalf("cancelled query exposed partial results: %+v, %v", result, err)
			}
		}
	}
}

func TestLoadRejectsPartialDatasets(t *testing.T) {
	tests := []struct {
		name, input string
		engine      Engine
		limit       int
	}{
		{"malformed tail", fixture + "broken", Row, 10},
		{"columnar malformed tail", fixture + "broken", Columnar, 10},
		{"event limit", fixture, Row, 3},
		{"columnar event limit", fixture, Columnar, 3},
		{"invalid engine", fixture, "other", 10},
		{"invalid limit", fixture, Row, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := Load(context.Background(), strings.NewReader(test.input), test.engine, test.limit)
			if err == nil || data != nil {
				t.Fatalf("expected no dataset on failure, got %v, %v", data, err)
			}
		})
	}
	if _, err := Load(context.Background(), strings.NewReader(fixture), Row, 4); err != nil {
		t.Fatalf("exact event limit should succeed: %v", err)
	}
}

func TestServiceDictionaryBoundary(t *testing.T) {
	var input strings.Builder
	for i := 0; i < MaxServices; i++ {
		fmt.Fprintf(&input, "{\"timestamp_us\":%d,\"service\":\"s%d\",\"duration_us\":1,\"status\":200}\n", i, i)
	}
	data := loadFixture(t, input.String(), Columnar)
	result, err := data.Query(context.Background(), Filter{Service: "s65535"})
	if err != nil || result.Aggregate.Count != 1 || data.ServiceCount() != MaxServices {
		t.Fatalf("last uint16 service ID failed: %+v, %v", result, err)
	}
	input.WriteString(`{"timestamp_us":65536,"service":"overflow","duration_us":1,"status":200}`)
	rejected, err := Load(context.Background(), strings.NewReader(input.String()), Columnar, MaxServices+1)
	if err == nil || rejected != nil || !strings.Contains(err.Error(), "distinct services") {
		t.Fatalf("service dictionary wrapped: %v, %v", rejected, err)
	}
}

func generatedInput(t testing.TB, events int) []byte {
	t.Helper()
	var output bytes.Buffer
	err := generator.Generate(context.Background(), &output, generator.Config{
		Events: int64(events), Services: 16, Seed: 42,
		Start: time.Unix(0, 0), Interval: time.Microsecond, ErrorPercent: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestEnginesAgreeAcrossGeneratedQueries(t *testing.T) {
	input := generatedInput(t, 2000)
	row := loadFixture(t, string(input), Row)
	columnar := loadFixture(t, string(input), Columnar)
	rng := rand.New(rand.NewSource(17))
	for i := 0; i < 200; i++ {
		from := int64(rng.Intn(2200))
		filter := Filter{FromUS: from, ToUS: pointer(from + int64(rng.Intn(2200)))}
		if i%3 == 0 {
			filter.Service = fmt.Sprintf("service-%03d", 1+rng.Intn(18))
		}
		if i%5 == 0 {
			filter.Status = 500
		}
		a, err := row.Query(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		b, err := columnar.Query(context.Background(), filter)
		if err != nil || !reflect.DeepEqual(a.Aggregate, b.Aggregate) || a.Stats.RowsExamined != b.Stats.RowsExamined {
			t.Fatalf("engines disagree on filter %+v: %+v != %+v (%v)", filter, a, b, err)
		}
	}
}

func TestConcurrentQueries(t *testing.T) {
	for _, engine := range []Engine{Row, Columnar} {
		data := loadFixture(t, fixture, engine)
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				for range 20 {
					result, err := data.Query(context.Background(), Filter{})
					if err != nil || result.Aggregate.Count != 4 || result.Aggregate.DurationSumUS != 70 {
						t.Errorf("concurrent query failed: %+v, %v", result, err)
						return
					}
				}
			})
		}
		workers.Wait()
	}
}

func TestAccumulatorDetectsOverflow(t *testing.T) {
	a := accumulator{sum: math.MaxUint64}
	if err := a.add(1, 200); err == nil || a.sum != math.MaxUint64 || a.count != 0 {
		t.Fatal("overflow was not rejected before mutation")
	}
}

func TestJSONResultHasExplicitNullsForNoMatches(t *testing.T) {
	data := loadFixture(t, fixture, Row)
	result, err := data.Query(context.Background(), Filter{Service: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.Aggregate)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"mean_duration_us", "min_duration_us", "max_duration_us"} {
		if !bytes.Contains(encoded, []byte(`"`+field+`":null`)) {
			t.Fatalf("missing null for %s: %s", field, encoded)
		}
	}
}

func TestEventDurationSumExceedsUint32(t *testing.T) {
	var input bytes.Buffer
	encoder := json.NewEncoder(&input)
	for i := range 2 {
		if err := encoder.Encode(telemetry.Event{TimestampUS: int64(i), Service: "a", DurationUS: math.MaxUint32, Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	for _, engine := range []Engine{Row, Columnar} {
		data := loadFixture(t, input.String(), engine)
		result, err := data.Query(context.Background(), Filter{})
		if err != nil || result.Aggregate.DurationSumUS != 2*uint64(math.MaxUint32) {
			t.Fatalf("sum truncated to uint32: %+v, %v", result, err)
		}
	}
}
