package query

import (
	"context"
	"testing"
)

func BenchmarkQuery(b *testing.B) {
	const events = 100000
	input := string(generatedInput(b, events))
	cases := []struct {
		name   string
		filter Filter
	}{
		{"all", Filter{}},
		{"time_1pct", Filter{FromUS: 50000, ToUS: pointer(int64(51000))}},
		{"service_errors", Filter{Service: "service-001", Status: 500}},
	}
	for _, engine := range []Engine{Row, Columnar} {
		data := loadFixture(b, input, engine)
		for _, test := range cases {
			b.Run(string(engine)+"/"+test.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					result, err := data.Query(context.Background(), test.filter)
					if err != nil {
						b.Fatal(err)
					}
					if result.Stats.RowsExamined != events {
						b.Fatal("benchmark stopped measuring a full scan")
					}
				}
				b.ReportMetric(events, "rows/op")
			})
		}
	}
}
