package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

func TestCatalogUsesOneSnapshotAndSharedColumns(t *testing.T) {
	catalog, err := LoadCatalog(context.Background(), strings.NewReader(fixture), 4)
	if err != nil {
		t.Fatal(err)
	}
	columnar, _ := catalog.Engine(Columnar)
	indexed, _ := catalog.Engine(Indexed)
	if &columnar.timestamps[0] != &indexed.timestamps[0] || &columnar.durations[0] != &indexed.durations[0] {
		t.Fatal("index duplicated immutable columns")
	}
	var expected Aggregate
	for _, engine := range []Engine{Row, Columnar, Indexed} {
		data, err := catalog.Engine(engine)
		if err != nil {
			t.Fatal(err)
		}
		result, err := data.Query(context.Background(), Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if engine == Row {
			expected = result.Aggregate
		} else if !reflect.DeepEqual(expected, result.Aggregate) {
			t.Fatal("catalog layouts disagree")
		}
	}
	meta := catalog.Metadata()
	if meta.Rows != 4 || *meta.MinUS != 1 || *meta.MaxUS != 3 ||
		!reflect.DeepEqual(meta.Services, []string{"a", "b"}) ||
		!reflect.DeepEqual(meta.Statuses, []uint16{200, 404, 500, 503}) {
		t.Fatalf("incorrect metadata: %+v", meta)
	}
	meta.Services[0] = "changed"
	meta.Statuses[0] = 599
	if catalog.Metadata().Services[0] != "a" || catalog.Metadata().Statuses[0] != 200 {
		t.Fatal("metadata exposes mutable catalog slices")
	}
	if _, err := catalog.Engine("invalid"); err == nil {
		t.Fatal("unknown catalog engine accepted")
	}
}

func TestCatalogRejectsFailureAndSupportsEmptyData(t *testing.T) {
	if catalog, err := LoadCatalog(context.Background(), strings.NewReader(fixture+"broken"), 10); err == nil || catalog != nil {
		t.Fatal("catalog exposed partial data")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if catalog, err := LoadCatalog(ctx, strings.NewReader(fixture), 4); !errors.Is(err, context.Canceled) || catalog != nil {
		t.Fatal("cancelled catalog load succeeded")
	}
	empty, err := LoadCatalog(context.Background(), strings.NewReader(""), 10)
	if err != nil || empty.Metadata().MinUS != nil || empty.Metadata().Rows != 0 {
		t.Fatalf("invalid empty metadata: %v, %v", empty, err)
	}
}

func TestProfileCountsAgreeWithAggregate(t *testing.T) {
	data := loadFixture(t, fixture, Indexed)
	filters := []Filter{
		{FromUS: 1}, {FromUS: 2, ToUS: pointer(int64(3))},
		{Service: "a", Status: 503}, {Service: "missing"},
		{FromUS: 2, ToUS: pointer(int64(2))}, {FromUS: 100},
	}
	for _, filter := range filters {
		for _, buckets := range []int{1, 7, 100, MaxBuckets} {
			profile, err := data.Profile(context.Background(), filter, buckets)
			if err != nil {
				t.Fatal(err)
			}
			result, err := data.Query(context.Background(), filter)
			if err != nil {
				t.Fatal(err)
			}
			var count, errors, histogramCount, serviceCount uint64
			for _, bucket := range profile.Timeline {
				count += bucket.Count
				errors += bucket.ErrorCount
			}
			for _, bucket := range profile.Histogram {
				histogramCount += bucket.Count
			}
			for _, service := range profile.Services {
				serviceCount += service.Count
			}
			if count != result.Aggregate.Count || errors != result.Aggregate.ErrorCount ||
				histogramCount != count || serviceCount != count ||
				profile.RowsExamined != result.Stats.RowsExamined || len(profile.Timeline) > buckets {
				t.Fatalf("profile disagrees with aggregate: %+v vs %+v", profile, result)
			}
		}
	}
	profile, _ := data.Profile(context.Background(), Filter{FromUS: 1}, 100)
	if len(profile.Timeline) != 3 || profile.Timeline[1].Count != 2 || profile.Timeline[1].ErrorCount != 2 ||
		profile.Services[0].Service != "a" || profile.Services[0].MeanDurationUS != 50.0/3 {
		t.Fatalf("incorrect exact chart values: %+v", profile)
	}
}

func TestProfileHistogramEdges(t *testing.T) {
	durations := []uint32{0, 999, 1000, 4999, 5000, 9999, 10000, 49999, 50000, 99999, 100000, 249999, 250000, 499999, 500000, math.MaxUint32}
	var input bytes.Buffer
	for i, duration := range durations {
		if err := json.NewEncoder(&input).Encode(telemetry.Event{TimestampUS: int64(i), Service: "a", DurationUS: duration, Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	data := loadFixture(t, input.String(), Indexed)
	profile, err := data.Profile(context.Background(), Filter{}, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range profile.Histogram {
		if bucket.Count != 2 {
			t.Fatalf("histogram boundary error: %+v", profile.Histogram)
		}
	}
}

func TestProfileMaximumTimestampDoesNotOverflow(t *testing.T) {
	input := `{"timestamp_us":0,"service":"a","duration_us":0,"status":200}
{"timestamp_us":9223372036854775807,"service":"a","duration_us":1,"status":500}`
	data := loadFixture(t, input, Indexed)
	profile, err := data.Profile(context.Background(), Filter{}, MaxBuckets)
	if err != nil {
		t.Fatal(err)
	}
	last := profile.Timeline[len(profile.Timeline)-1]
	if profile.Timeline[0].Count != 1 || last.Count != 1 || last.ToUS != "9223372036854775808" {
		t.Fatalf("large-range bucket arithmetic failed: %+v", profile.Timeline)
	}
}

func TestProfileLimitsAndCancellation(t *testing.T) {
	data := loadFixture(t, string(generatedInput(t, 2000)), Indexed)
	for _, buckets := range []int{0, MaxBuckets + 1} {
		if _, err := data.Profile(context.Background(), Filter{}, buckets); err == nil {
			t.Fatal("invalid bucket limit accepted")
		}
	}
	if _, err := data.Profile(context.Background(), Filter{FromUS: -1}, 10); err == nil {
		t.Fatal("invalid profile filter accepted")
	}
	row := loadFixture(t, fixture, Row)
	if _, err := row.Profile(context.Background(), Filter{}, 10); err == nil {
		t.Fatal("row profile should not silently use absent columns")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, err := data.Profile(&cancelOnCheck{Context: ctx, cancel: cancel}, Filter{}, 10)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, Profile{}) {
		t.Fatal("cancelled profile exposed partial charts")
	}
	var input strings.Builder
	for i := range 21 {
		fmt.Fprintf(&input, "{\"timestamp_us\":%d,\"service\":\"s%02d\",\"duration_us\":1,\"status\":200}\n", i, i)
	}
	profile, err := loadFixture(t, input.String(), Indexed).Profile(context.Background(), Filter{}, 10)
	if err != nil || len(profile.Services) != 20 || !profile.ServicesTruncated ||
		profile.Services[0].Service != "s00" || profile.Services[19].Service != "s19" {
		t.Fatalf("service limit/order incorrect: %+v, %v", profile, err)
	}
}

func FuzzProfileRange(f *testing.F) {
	f.Add(int64(0), int64(math.MaxInt64), false, uint16(240))
	f.Add(int64(math.MaxInt64), int64(math.MaxInt64), false, uint16(1))
	f.Add(int64(0), int64(1), true, uint16(100))
	input := `{"timestamp_us":0,"service":"a","duration_us":0,"status":200}
{"timestamp_us":9223372036854775807,"service":"a","duration_us":4294967295,"status":500}`
	data := loadFixture(f, input, Indexed)
	f.Fuzz(func(t *testing.T, from, to int64, bounded bool, rawBuckets uint16) {
		filter := Filter{FromUS: from}
		if bounded {
			filter.ToUS = &to
		}
		if filter.Validate() != nil {
			t.Skip()
		}
		buckets := int(rawBuckets)%MaxBuckets + 1
		profile, err := data.Profile(context.Background(), filter, buckets)
		if err != nil {
			t.Fatal(err)
		}
		aggregate, err := data.Query(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		var count, previousEnd uint64
		for i, bucket := range profile.Timeline {
			start, err := strconv.ParseUint(bucket.FromUS, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			end, err := strconv.ParseUint(bucket.ToUS, 10, 64)
			if err != nil || start >= end || (i > 0 && start != previousEnd) {
				t.Fatalf("overflow, overlap, or gap in time buckets: %+v (%v)", bucket, err)
			}
			previousEnd = end
			count += bucket.Count
		}
		if count != aggregate.Aggregate.Count || len(profile.Timeline) > buckets {
			t.Fatal("profile lost events or exceeded its payload bound")
		}
	})
}
