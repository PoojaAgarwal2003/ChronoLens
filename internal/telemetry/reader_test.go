package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

const validRecord = `{"timestamp_us":1,"service":"api","duration_us":10,"status":200}`

func TestReadJSONL(t *testing.T) {
	tests := []struct {
		name, input string
		count       int
	}{
		{"empty", "", 0},
		{"no final newline", validRecord, 1},
		{"CRLF", validRecord + "\r\n", 1},
		{"equal timestamps", validRecord + "\n" + validRecord + "\n", 2},
		{"maximum record", validRecord + strings.Repeat(" ", MaxRecordBytes-len(validRecord)) + "\r\n", 1},
		{"reordered keys", `{"status":599,"duration_us":4294967295,"service":"api","timestamp_us":0}`, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			count := 0
			err := ReadJSONL(context.Background(), strings.NewReader(test.input), func(event Event) error {
				count++
				return event.Validate()
			})
			if err != nil || count != test.count {
				t.Fatalf("count=%d, err=%v; want %d records", count, err, test.count)
			}
		})
	}
}

func TestReadJSONLRejectsInvalidRecords(t *testing.T) {
	tests := []struct{ name, input string }{
		{"blank line", "\n"},
		{"whitespace", " \t\n"},
		{"truncated", `{"timestamp_us":1`},
		{"missing field", `{"timestamp_us":1,"service":"api","status":200}`},
		{"unknown field", strings.Replace(validRecord, `"status":200`, `"status":200,"extra":1`, 1)},
		{"case mismatch", strings.Replace(validRecord, "timestamp_us", "TIMESTAMP_US", 1)},
		{"duplicate field", strings.Replace(validRecord, `"status":200`, `"status":200,"status":500`, 1)},
		{"escaped duplicate", strings.Replace(validRecord, `"status":200`, `"status":200,"\u0073tatus":500`, 1)},
		{"trailing object", validRecord + validRecord},
		{"trailing garbage", validRecord + "broken"},
		{"array", "[" + validRecord + "]"},
		{"null object", "null"},
		{"null number", strings.Replace(validRecord, `"status":200`, `"status":null`, 1)},
		{"null string", strings.Replace(validRecord, `"api"`, "null", 1)},
		{"empty service", strings.Replace(validRecord, `"api"`, `""`, 1)},
		{"padded service", strings.Replace(validRecord, `"api"`, `" api"`, 1)},
		{"long service", strings.Replace(validRecord, "api", strings.Repeat("x", MaxServiceBytes+1), 1)},
		{"negative timestamp", strings.Replace(validRecord, `"timestamp_us":1`, `"timestamp_us":-1`, 1)},
		{"timestamp overflow", strings.Replace(validRecord, `"timestamp_us":1`, `"timestamp_us":9223372036854775808`, 1)},
		{"duration overflow", strings.Replace(validRecord, `"duration_us":10`, `"duration_us":4294967296`, 1)},
		{"negative duration", strings.Replace(validRecord, `"duration_us":10`, `"duration_us":-1`, 1)},
		{"fractional duration", strings.Replace(validRecord, `"duration_us":10`, `"duration_us":1.5`, 1)},
		{"string number", strings.Replace(validRecord, `"duration_us":10`, `"duration_us":"10"`, 1)},
		{"low status", strings.Replace(validRecord, `"status":200`, `"status":99`, 1)},
		{"high status", strings.Replace(validRecord, `"status":200`, `"status":600`, 1)},
		{"invalid UTF-8", strings.Replace(validRecord, "api", "a\xff", 1)},
		{"oversized record", validRecord + strings.Repeat(" ", MaxRecordBytes)},
		{"unsorted", validRecord + "\n" + strings.Replace(validRecord, `"timestamp_us":1`, `"timestamp_us":0`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ReadJSONL(context.Background(), strings.NewReader(test.input), func(Event) error { return nil })
			if err == nil || !strings.Contains(err.Error(), "line ") {
				t.Fatalf("expected a line-numbered error, got %v", err)
			}
		})
	}
}

type brokenReader struct{ err error }

func (r brokenReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadJSONLPropagatesFailures(t *testing.T) {
	failure := errors.New("simulated read failure")
	if err := ReadJSONL(context.Background(), brokenReader{failure}, func(Event) error { return nil }); !errors.Is(err, failure) {
		t.Fatalf("expected reader failure, got %v", err)
	}
	err := ReadJSONL(context.Background(), strings.NewReader(validRecord), func(Event) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("expected visitor failure, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visited := 0
	err = ReadJSONL(ctx, strings.NewReader(validRecord+"\n"+validRecord), func(Event) error {
		visited++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visited != 1 {
		t.Fatalf("visited=%d, error=%v; wanted cancellation after one record", visited, err)
	}
}

func TestValidateServiceUTF8(t *testing.T) {
	if err := ValidateService("a\xff"); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
}

func FuzzReadJSONL(f *testing.F) {
	for _, seed := range []string{validRecord, "", validRecord + "\r\n", `{"status":null}`, `[]`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64*1024 {
			t.Skip()
		}
		var previous int64
		err := ReadJSONL(context.Background(), strings.NewReader(input), func(event Event) error {
			if err := event.Validate(); err != nil {
				t.Fatalf("visitor received invalid event: %v", err)
			}
			if event.TimestampUS < previous {
				t.Fatal("visitor received unordered events")
			}
			previous = event.TimestampUS
			return nil
		})
		if err == io.EOF {
			t.Fatal("empty input must succeed; malformed input must have context")
		}
	})
}

func ExampleReadJSONL() {
	err := ReadJSONL(context.Background(), strings.NewReader(validRecord), func(event Event) error {
		fmt.Println(event.Service, event.Status)
		return nil
	})
	fmt.Println(err)
	// Output:
	// api 200
	// <nil>
}
