package query

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

// A deliberately slow oracle: scan every row, locate histogram/timeline bins
// linearly and group by service name, independently of the optimized ID path.
func referenceProfile(t *testing.T, data *Dataset, filter Filter, buckets int) Profile {
	t.Helper()
	want := Profile{Timeline: []TimeBucket{}, Services: []ServiceSummary{}}
	for _, label := range []string{"<1 ms", "1-5 ms", "5-10 ms", "10-50 ms", "50-100 ms", "100-250 ms", "250-500 ms", ">=500 ms"} {
		want.Histogram = append(want.Histogram, HistogramBucket{Label: label})
	}
	query, err := data.Query(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	want.RowsExamined, want.IndexComparisons = query.Stats.RowsExamined, query.Stats.IndexComparisons
	from, to := uint64(filter.FromUS), uint64(data.timestamps[data.Len()-1])+1
	if filter.ToUS != nil {
		to = uint64(*filter.ToUS)
	}
	if to <= from {
		return want
	}
	width := (to-from-1)/uint64(buckets) + 1
	for lower := from; lower < to; lower += width {
		want.Timeline = append(want.Timeline, TimeBucket{FromUS: strconv.FormatUint(lower, 10), ToUS: strconv.FormatUint(min(lower+width, to), 10)})
	}
	type totals struct{ count, errors, sum uint64 }
	services := map[string]totals{}
	for i, ts := range data.timestamps {
		name, status, duration := data.names[data.serviceIDs[i]], data.statuses[i], data.durations[i]
		if ts < filter.FromUS || (filter.ToUS != nil && ts >= *filter.ToUS) ||
			(filter.Service != "" && name != filter.Service) || (filter.Status != 0 && status != filter.Status) {
			continue
		}
		for j := range want.Timeline {
			bin := &want.Timeline[j]
			lower, _ := strconv.ParseUint(bin.FromUS, 10, 64)
			upper, _ := strconv.ParseUint(bin.ToUS, 10, 64)
			if uint64(ts) >= lower && uint64(ts) < upper {
				bin.Count++
				if status >= 500 {
					bin.ErrorCount++
				}
				break
			}
		}
		for j, upper := range []uint64{1000, 5000, 10000, 50000, 100000, 250000, 500000, 1 << 32} {
			if uint64(duration) < upper {
				want.Histogram[j].Count++
				break
			}
		}
		s := services[name]
		s.count++
		s.sum += uint64(duration)
		if status >= 500 {
			s.errors++
		}
		services[name] = s
	}
	for name, s := range services {
		want.Services = append(want.Services, ServiceSummary{name, s.count, s.errors, float64(s.sum) / float64(s.count)})
	}
	sort.Slice(want.Services, func(i, j int) bool {
		a, b := want.Services[i], want.Services[j]
		return a.Count > b.Count || (a.Count == b.Count && a.Service < b.Service)
	})
	if len(want.Services) > MaxResultServices {
		want.Services = want.Services[:MaxResultServices]
		want.ServicesTruncated = true
	}
	return want
}

func TestProfileExactReference(t *testing.T) {
	data := cardinalityFixture(t, 65540, 65536)
	for _, filter := range []Filter{
		{}, {FromUS: 65535, ToUS: pointer(int64(65539))}, {Service: "service-65535"},
		{Service: "missing"}, {Status: 500}, {FromUS: 65540}, {FromUS: 3, ToUS: pointer(int64(3))},
	} {
		for _, buckets := range []int{1, 7, 100, 240} {
			got, err := data.Profile(context.Background(), filter, buckets)
			want := referenceProfile(t, data, filter, buckets)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("filter=%+v buckets=%d: profile differs from exact reference (%v)", filter, buckets, err)
			}

		}
	}
}

func TestProfileConcurrentAccumulatorPaths(t *testing.T) {
	data := cardinalityFixture(t, 65540, 65536)
	for i, filter := range []Filter{
		{}, {FromUS: 65535, ToUS: pointer(int64(65539))},
		{Service: "service-65535"}, {Service: "missing"}, {Status: 201},
	} {
		want := referenceProfile(t, data, filter, 7)
		for worker := range 4 {
			t.Run(fmt.Sprintf("path_%d/worker_%d", i, worker), func(t *testing.T) {
				t.Parallel()
				for range 5 {
					got, err := data.Profile(context.Background(), filter, 7)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("concurrent profile differs from reference: %v", err)
					}
				}
			})
		}
	}
}
