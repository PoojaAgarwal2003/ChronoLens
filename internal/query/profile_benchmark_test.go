package query

import (
	"context"
	"fmt"
	"testing"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

// Round-robin IDs exercise the full uint16 dictionary, including ID 65535.
// This deterministic fixture has no RNG; construction is outside b.Loop.
func cardinalityFixture(t testing.TB, events, services int) *Dataset {
	t.Helper()
	data := &Dataset{engine: Indexed, dictionary: make(map[string]uint16)}
	names := make([]string, services)
	for i := range names {
		names[i] = fmt.Sprintf("service-%05d", i)
	}
	for i := range events {
		status := uint16(200)
		if i%20 == 0 {
			status = 500
		}
		if err := data.appendEvent(telemetry.Event{
			TimestampUS: int64(i), Service: names[i%services],
			DurationUS: uint32(i*7919) % 750000, Status: status,
		}, events); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func BenchmarkProfile(b *testing.B) {
	for _, services := range []int{16, 65536} {
		b.Run(fmt.Sprintf("services_%d", services), func(b *testing.B) {
			var data *Dataset
			if services == 16 {
				data = loadFixture(b, string(generatedInput(b, 1000000)), Indexed)
			} else {
				data = cardinalityFixture(b, 1000000, services)
			}
			for _, tc := range []struct {
				name   string
				filter Filter
			}{
				{"full", Filter{}},
				{"time_1pct", Filter{FromUS: 500000, ToUS: pointer(int64(510000))}},
				{"sparse", Filter{FromUS: 65535, ToUS: pointer(int64(65539))}},
				{"service", Filter{Service: data.names[len(data.names)-1]}},
				{"unknown", Filter{Service: "missing"}},
				{"empty", Filter{FromUS: 1000001}},
			} {
				b.Run(tc.name, func(b *testing.B) {
					expected, err := data.Query(context.Background(), tc.filter)
					if err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					for b.Loop() {
						profile, err := data.Profile(context.Background(), tc.filter, 100)
						if err != nil || profile.RowsExamined != expected.Stats.RowsExamined {
							b.Fatalf("profile failed or changed candidate visits: %v", err)
						}
					}
					b.ReportMetric(float64(expected.Stats.RowsExamined), "rows/op")
				})
			}
		})
	}
}
