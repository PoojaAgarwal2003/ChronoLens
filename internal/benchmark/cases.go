package benchmark

import (
	"fmt"
	"math"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

func makeCases(metadata query.Metadata) ([]Case, error) {
	if metadata.Rows == 0 || metadata.MinUS == nil || metadata.MaxUS == nil || len(metadata.Services) == 0 {
		return nil, fmt.Errorf("input dataset is empty")
	}
	if *metadata.MinUS < 0 || *metadata.MaxUS < *metadata.MinUS {
		return nil, fmt.Errorf("invalid dataset timestamp bounds")
	}
	// Use the inclusive integer domain [min, max+1). Unsigned arithmetic
	// represents MaxInt64+1, and division before centering avoids multiplication.
	span := uint64(*metadata.MaxUS) - uint64(*metadata.MinUS) + 1
	cases := make([]Case, 0, 5)
	for i, divisor := range []uint64{1000, 100, 10, 1} {
		width := (span-1)/divisor + 1
		from := uint64(*metadata.MinUS) + (span-width)/2
		to := from + width
		filter := query.Filter{FromUS: int64(from)}
		if to <= math.MaxInt64 {
			endpoint := int64(to)
			filter.ToUS = &endpoint
		}
		cases = append(cases, Case{
			Name:        []string{"time_0.1pct", "time_1pct", "time_10pct", "time_100pct"}[i],
			SpanDivisor: divisor, ActualWidthUS: width, Filter: filter,
		})
	}
	cases = append(cases, Case{
		Name: "service_only", ActualWidthUS: span,
		Filter:          query.Filter{Service: metadata.Services[0]},
		TimeMatchedRows: uint64(metadata.Rows),
	})
	return cases, nil
}
