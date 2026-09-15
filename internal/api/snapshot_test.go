package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/snapshot"
)

func TestSnapshotMetadataAndQueryParity(t *testing.T) {
	var packed bytes.Buffer
	if _, err := snapshot.WriteJSONL(context.Background(), &packed, strings.NewReader(testData), 10); err != nil {
		t.Fatal(err)
	}
	var reference queryResponse
	for _, format := range []query.InputFormat{query.JSONLFormat, query.SnapshotFormat} {
		input := []byte(testData)
		if format == query.SnapshotFormat {
			input = packed.Bytes()
		}
		catalog, err := query.LoadCatalogFormat(context.Background(), bytes.NewReader(input), 10, format)
		if err != nil {
			t.Fatal(err)
		}
		server, err := New(catalog, fstest.MapFS{"index.html": {Data: []byte("<title>fixture</title>")}}, "fixture", 0)
		if err != nil {
			t.Fatal(err)
		}
		metaResponse := call(server, http.MethodGet, "/api/meta", "")
		var meta metadataResponse
		if err := json.Unmarshal(metaResponse.Body.Bytes(), &meta); err != nil ||
			metaResponse.Code != http.StatusOK || meta.InputFormat != format || meta.Rows != 4 {
			t.Fatalf("incorrect format metadata: %s (%v)", metaResponse.Body, err)
		}
		response := call(server, http.MethodPost, "/api/query", validRequest)
		var result queryResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK {
			t.Fatalf("query failed: %s (%v)", response.Body, err)
		}
		if format == query.JSONLFormat {
			reference = result
		} else if !reflect.DeepEqual(result.Result, reference.Result) || !reflect.DeepEqual(result.Profile, reference.Profile) {
			t.Fatal("input representation changed HTTP aggregate or profile semantics")
		}
	}
}
