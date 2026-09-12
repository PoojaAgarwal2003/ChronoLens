package query

import (
	"context"
	"errors"
	"math"
	"math/bits"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestIndexedBoundariesAndStats(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		filter Filter
		visits int
	}{
		{"all", fixture, Filter{}, 4},
		{"ties on both boundaries", fixture, Filter{FromUS: 2, ToUS: pointer(int64(3))}, 2},
		{"ties at upper boundary excluded", fixture, Filter{ToUS: pointer(int64(2))}, 1},
		{"zero width", fixture, Filter{FromUS: 2, ToUS: pointer(int64(2))}, 0},
		{"no upper bound", fixture, Filter{FromUS: 2}, 3},
		{"before data", fixture, Filter{ToUS: pointer(int64(1))}, 0},
		{"after data", fixture, Filter{FromUS: 4}, 0},
		{"service filtering does not change candidate visits", fixture, Filter{FromUS: 2, ToUS: pointer(int64(3)), Service: "a", Status: 503}, 2},
		{"missing service", fixture, Filter{Service: "missing"}, 4},
		{"empty dataset", "", Filter{}, 0},
		{"maximum timestamp included", `{"timestamp_us":9223372036854775807,"service":"a","duration_us":1,"status":500}`, Filter{FromUS: math.MaxInt64}, 1},
		{"maximum timestamp excluded", `{"timestamp_us":9223372036854775807,"service":"a","duration_us":1,"status":500}`, Filter{ToUS: pointer(int64(math.MaxInt64))}, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := loadFixture(t, test.input, Row)
			indexed := loadFixture(t, test.input, Indexed)
			want, err := row.Query(context.Background(), test.filter)
			if err != nil {
				t.Fatal(err)
			}
			got, err := indexed.Query(context.Background(), test.filter)
			if err != nil || !reflect.DeepEqual(got.Aggregate, want.Aggregate) {
				t.Fatalf("indexed result differs from reference: %+v / %+v (%v)", got, want, err)
			}
			if got.Stats.Engine != Indexed || got.Stats.TotalRows != row.Len() ||
				got.Stats.RowsExamined != test.visits || got.Stats.RowsSkipped != row.Len()-test.visits {
				t.Fatalf("incorrect indexed work accounting: %+v", got.Stats)
			}
			if got.Stats.IndexComparisons > 2*bits.Len(uint(row.Len())) {
				t.Fatalf("search exceeded logarithmic comparison bound: %+v", got.Stats)
			}
			if row.Len() > 0 && got.Stats.IndexComparisons == 0 {
				t.Fatal("nonempty index lookup did not account for comparisons")
			}
		})
	}
}

func TestIndexedGeneratedSelectivity(t *testing.T) {
	const count = 10000
	input := string(generatedInput(t, count))
	row := loadFixture(t, input, Row)
	indexed := loadFixture(t, input, Indexed)
	rng := rand.New(rand.NewSource(123))
	for range 300 {
		from := int64(rng.Intn(count + 100))
		to := from + int64(rng.Intn(count+100))
		filter := Filter{FromUS: from, ToUS: &to}
		if rng.Intn(2) == 0 {
			filter.Service = "service-001"
		}
		if rng.Intn(2) == 0 {
			filter.Status = 500
		}
		want, err := row.Query(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		got, err := indexed.Query(context.Background(), filter)
		if err != nil || !reflect.DeepEqual(got.Aggregate, want.Aggregate) {
			t.Fatalf("indexed query differs for %+v (%v)", filter, err)
		}
		visits := max(int64(0), min(to, int64(count))-from)
		if got.Stats.RowsExamined != int(visits) {
			t.Fatalf("got %d candidate visits, want %d", got.Stats.RowsExamined, visits)
		}
	}
}

func TestIndexedValidationAndCancellation(t *testing.T) {
	data := loadFixture(t, string(generatedInput(t, 2000)), Indexed)
	for _, filter := range []Filter{{FromUS: -1}, {FromUS: 4, ToUS: pointer(int64(3))}, {Status: 600}} {
		if _, err := data.Query(context.Background(), filter); err == nil {
			t.Fatal("indexed engine bypassed filter validation")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := data.Query(&cancelOnCheck{Context: ctx, cancel: cancel}, Filter{})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("cancelled index query exposed partial output: %+v, %v", result, err)
	}
	unsorted := strings.Replace(fixture, `"timestamp_us":3`, `"timestamp_us":0`, 1)
	if data, err := Load(context.Background(), strings.NewReader(unsorted), Indexed, 10); err == nil || data != nil {
		t.Fatal("index accepted unsorted input")
	}
}

func TestIndexedConcurrentQueries(t *testing.T) {
	data := loadFixture(t, fixture, Indexed)
	for i := range 8 {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			t.Parallel()
			for range 100 {
				got, err := data.Query(context.Background(), Filter{FromUS: 2, ToUS: pointer(int64(3))})
				if err != nil || got.Aggregate.Count != 2 || got.Stats.RowsExamined != 2 {
					t.Fatalf("concurrent index query failed: %+v, %v", got, err)
				}
			}
		})
	}
}
