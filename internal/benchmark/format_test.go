package benchmark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/snapshot"
)

func TestBenchmarkSnapshotParityAndFingerprint(t *testing.T) {
	var encoded bytes.Buffer
	if _, err := snapshot.WriteJSONL(context.Background(), &encoded, strings.NewReader(fixture), 5); err != nil {
		t.Fatal(err)
	}
	baseline, err := Run(context.Background(), quickConfig(fixtureFile(t, fixture)))
	if err != nil {
		t.Fatal(err)
	}
	config := quickConfig(fixtureFile(t, encoded.String()))
	config.InputFormat = query.SnapshotFormat
	report, err := Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256(encoded.Bytes()))
	if report.Source.InputFormat != query.SnapshotFormat || report.Source.SHA256 != wantHash ||
		report.Source.Bytes != int64(encoded.Len()) || report.Source.Events != baseline.Source.Events ||
		!reflect.DeepEqual(report.Cases, baseline.Cases) {
		t.Fatalf("incorrect snapshot provenance or cases: %+v", report.Source)
	}
	for i, engine := range report.Engines {
		if engine.Load.SHA256 != wantHash || engine.Load.Bytes != int64(encoded.Len()) {
			t.Fatal("snapshot source was not fully fingerprinted for each engine")
		}
		for j, result := range engine.Cases {
			if !reflect.DeepEqual(result.Result, baseline.Engines[i].Cases[j].Result) {
				t.Fatal("representation changed aggregate or candidate statistics")
			}
		}
	}
}

func TestBenchmarkFormatFailureNeverReturnsPartialReport(t *testing.T) {
	var encoded bytes.Buffer
	if _, err := snapshot.WriteJSONL(context.Background(), &encoded, strings.NewReader(fixture), 5); err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Clone(encoded.Bytes())
	corrupt[len(corrupt)-1] ^= 1
	for _, test := range []struct {
		input  string
		format query.InputFormat
		limit  int
	}{
		{fixture, query.SnapshotFormat, 5},
		{encoded.String(), query.JSONLFormat, 5},
		{string(corrupt), query.SnapshotFormat, 5},
		{encoded.String()[:encoded.Len()-1], query.SnapshotFormat, 5},
		{encoded.String(), query.SnapshotFormat, 4},
		{fixture, "auto", 5},
	} {
		config := quickConfig(fixtureFile(t, test.input))
		config.InputFormat, config.MaxEvents = test.format, test.limit
		if report, err := Run(context.Background(), config); err == nil || !reflect.DeepEqual(report, Report{}) {
			t.Fatalf("invalid input returned partial success: %+v (%v)", report, err)
		}
	}
}
