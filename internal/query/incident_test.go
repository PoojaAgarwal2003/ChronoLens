package query

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/generator"
)

func TestIncidentProducesExactSpikeAndRecovery(t *testing.T) {
	var input bytes.Buffer
	err := generator.Generate(context.Background(), &input, generator.Config{
		Events: 10000, Services: 16, Seed: 42, Start: time.Unix(0, 0),
		Interval: time.Millisecond, ErrorPercent: 5, Profile: generator.Incident,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCatalog(context.Background(), &input, 10000)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := catalog.Engine(Row)
	indexed, _ := catalog.Engine(Indexed)
	for _, phase := range []struct {
		from, to int64
		count    uint64
	}{
		{0, 4000000, 4000}, {4000000, 4500000, 2000}, {4500000, 8500000, 4000},
	} {
		filter := Filter{FromUS: phase.from, ToUS: &phase.to}
		want, err := row.Query(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		got, err := indexed.Query(context.Background(), filter)
		if err != nil || !reflect.DeepEqual(got.Aggregate, want.Aggregate) || got.Aggregate.Count != phase.count {
			t.Fatalf("incident phase disagrees across engines: %+v %+v (%v)", got, want, err)
		}
	}
	profile, err := indexed.Profile(context.Background(), Filter{ToUS: pointer(int64(8500000))}, 17)
	if err != nil {
		t.Fatal(err)
	}
	for i, bucket := range profile.Timeline {
		expected := uint64(500)
		if i == 8 {
			expected = 2000
			if bucket.ErrorCount < 1000 {
				t.Fatal("expected an observable error spike during congestion")
			}
		}
		if bucket.Count != expected {
			t.Fatalf("bucket %d: got %d events, want %d", i, bucket.Count, expected)
		}
	}
	if profile.Services[0].Service != "service-001" {
		t.Fatal("the targeted service should rank first by traffic")
	}
}
