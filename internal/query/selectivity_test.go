package query

import (
	"context"
	"testing"
)

func BenchmarkSelectivity(b *testing.B) {
	const events = 1000000
	input := string(generatedInput(b, events))
	cases := []struct {
		name  string
		width int
	}{
		{"0.1pct", 1000},
		{"1pct", 10000},
		{"10pct", 100000},
		{"100pct", events},
	}
	for _, engine := range []Engine{Row, Columnar, Indexed} {
		data := loadFixture(b, input, engine)
		for _, test := range cases {
			b.Run(string(engine)+"/"+test.name, func(b *testing.B) {
				from := int64((events - test.width) / 2)
				filter := Filter{FromUS: from, ToUS: pointer(from + int64(test.width))}
				visits := events
				if engine == Indexed {
					visits = test.width
				}
				b.ReportAllocs()
				for b.Loop() {
					result, err := data.Query(context.Background(), filter)
					if err != nil || result.Aggregate.Count != uint64(test.width) ||
						result.Stats.RowsExamined != visits {
						b.Fatalf("invalid benchmark result: %+v, %v", result, err)
					}
				}
				b.ReportMetric(float64(visits), "rows/op")
			})
		}
	}
}
