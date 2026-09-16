package query

import (
	"context"
	"fmt"
	"sort"
	"strconv"
)

const (
	MaxBuckets        = 240
	MaxResultServices = 20
)

type TimeBucket struct {
	FromUS     string `json:"from_us"`
	ToUS       string `json:"to_us"`
	Count      uint64 `json:"count"`
	ErrorCount uint64 `json:"error_count"`
}

type HistogramBucket struct {
	Label string `json:"label"`
	Count uint64 `json:"count"`
}

type ServiceSummary struct {
	Service        string  `json:"service"`
	Count          uint64  `json:"count"`
	ErrorCount     uint64  `json:"error_count"`
	MeanDurationUS float64 `json:"mean_duration_us"`
}

type Profile struct {
	Timeline          []TimeBucket      `json:"timeline"`
	Histogram         []HistogramBucket `json:"histogram"`
	Services          []ServiceSummary  `json:"services"`
	ServicesTruncated bool              `json:"services_truncated"`
	RowsExamined      int               `json:"rows_examined"`
	IndexComparisons  int               `json:"index_comparisons"`
}

// Profile makes a separate indexed pass for exact chart counts. Its candidate
// visits are reported separately rather than hidden in aggregate query timings.
func (d *Dataset) Profile(ctx context.Context, filter Filter, buckets int) (Profile, error) {
	if err := filter.Validate(); err != nil {
		return Profile{}, err
	}
	if buckets < 1 || buckets > MaxBuckets {
		return Profile{}, fmt.Errorf("buckets must be between 1 and %d", MaxBuckets)
	}
	if d.engine != Columnar && d.engine != Indexed {
		return Profile{}, fmt.Errorf("profiling requires a columnar dataset")
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	labels := []string{"<1 ms", "1-5 ms", "5-10 ms", "10-50 ms", "50-100 ms", "100-250 ms", "250-500 ms", ">=500 ms"}
	result := Profile{Timeline: []TimeBucket{}, Histogram: make([]HistogramBucket, len(labels)), Services: []ServiceSummary{}}
	for i, label := range labels {
		result.Histogram[i].Label = label
	}
	if d.Len() == 0 {
		return result, nil
	}
	first, end, comparisons := d.timeRange(filter)
	result.IndexComparisons = comparisons
	from, to := uint64(filter.FromUS), uint64(d.timestamps[d.Len()-1])+1
	if filter.ToUS != nil {
		to = uint64(*filter.ToUS)
	}
	if to <= from {
		return result, nil
	}
	// Unsigned offsets accommodate MaxInt64+1 as the final exclusive endpoint.
	// Division by a ceiling width avoids overflow from offset * bucketCount.
	span := to - from
	width := (span-1)/uint64(buckets) + 1
	count := int((span-1)/width + 1)
	result.Timeline = make([]TimeBucket, count)
	for i := range result.Timeline {
		lower := from + uint64(i)*width
		result.Timeline[i] = TimeBucket{
			FromUS: strconv.FormatUint(lower, 10),
			ToUS:   strconv.FormatUint(min(lower+width, to), 10),
		}
	}
	wantedID, known := d.dictionary[filter.Service]
	var single accumulator
	var dense []accumulator
	sparse := make(map[uint16]*accumulator)
	// Only allocate a dense table after a match, and never more entries than
	// candidate rows. Narrow windows on a large dictionary remain sparse.
	useDense := filter.Service == "" && len(d.names) <= end-first
	for i := first; i < end; i++ {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return Profile{}, err
			}
		}
		result.RowsExamined++
		if !matchesNumeric(d.timestamps[i], d.statuses[i], filter) ||
			(filter.Service != "" && (!known || d.serviceIDs[i] != wantedID)) {
			continue
		}
		bucket := &result.Timeline[(uint64(d.timestamps[i])-from)/width]
		bucket.Count++
		if d.statuses[i] >= 500 {
			bucket.ErrorCount++
		}
		result.Histogram[durationBucket(d.durations[i])].Count++
		id := d.serviceIDs[i]
		var summary *accumulator
		switch {
		case filter.Service != "":
			summary = &single
		case useDense:
			if dense == nil {
				dense = make([]accumulator, len(d.names))
			}
			summary = &dense[id]
		default:
			summary = sparse[id]
			if summary == nil {
				summary = &accumulator{}
				sparse[id] = summary
			}
		}
		if err := summary.add(d.durations[i], d.statuses[i]); err != nil {
			return Profile{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	appendService := func(id uint16, summary *accumulator) {
		result.Services = append(result.Services, ServiceSummary{
			Service: d.names[id], Count: summary.count, ErrorCount: summary.errors,
			MeanDurationUS: float64(summary.sum) / float64(summary.count),
		})
	}
	switch {
	case single.count != 0:
		appendService(wantedID, &single)
	case dense != nil:
		result.Services = make([]ServiceSummary, 0, len(dense))
		for id := range dense {
			if dense[id].count != 0 {
				appendService(uint16(id), &dense[id])
			}
		}
	default:
		result.Services = make([]ServiceSummary, 0, len(sparse))
		for id, summary := range sparse {
			appendService(id, summary)
		}
	}
	sort.Slice(result.Services, func(i, j int) bool {
		if result.Services[i].Count != result.Services[j].Count {
			return result.Services[i].Count > result.Services[j].Count
		}
		return result.Services[i].Service < result.Services[j].Service
	})
	if len(result.Services) > MaxResultServices {
		result.Services = result.Services[:MaxResultServices]
		result.ServicesTruncated = true
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	return result, nil
}

func durationBucket(duration uint32) int {
	switch {
	case duration < 1000:
		return 0
	case duration < 5000:
		return 1
	case duration < 10000:
		return 2
	case duration < 50000:
		return 3
	case duration < 100000:
		return 4
	case duration < 250000:
		return 5
	case duration < 500000:
		return 6
	default:
		return 7
	}
}
