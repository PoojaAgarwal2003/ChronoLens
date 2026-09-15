package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/generator"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

func jsonEvents(t testing.TB, events []telemetry.Event) []byte {
	t.Helper()
	var dst bytes.Buffer
	encoder := json.NewEncoder(&dst)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	return dst.Bytes()
}

func snapshotEvents(t testing.TB, events []telemetry.Event) []byte {
	t.Helper()
	var dst bytes.Buffer
	info, err := WriteJSONL(context.Background(), &dst, bytes.NewReader(jsonEvents(t, events)), max(1, len(events)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Events != uint64(len(events)) || info.Blocks != uint64((len(events)+blockRows-1)/blockRows) {
		t.Fatalf("unexpected writer info: %+v", info)
	}
	return dst.Bytes()
}

func readEvents(t testing.TB, data []byte, limit int) ([]telemetry.Event, Info) {
	t.Helper()
	var events []telemetry.Event
	info, err := Read(context.Background(), bytes.NewReader(data), limit, func(event telemetry.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return events, info
}

func discardEvent(telemetry.Event) error { return nil }

func requireError(t testing.TB, info Info, err error, contains string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %v; want containing %q", err, contains)
	}
	if info != (Info{}) {
		t.Fatalf("partial success-shaped info on error: %+v", info)
	}
}

func repairSHA(data []byte) {
	digest := sha256.Sum256(data[:len(data)-sha256.Size])
	copy(data[len(data)-sha256.Size:], digest[:])
}

func repairFirstCRC(data []byte) {
	end := headerBytes + framingBytes + int(binary.LittleEndian.Uint32(data[28:]))
	binary.LittleEndian.PutUint32(data[end:], crc32.Checksum(data[headerBytes:end], castagnoli))
}

func TestGoldenAndDeterminism(t *testing.T) {
	// Independently constructed using Python struct/hashlib and a bitwise
	// Castagnoli implementation, rather than this codec's encoder.
	const golden = "4348524f4e4f534e0100000000000000424c4b310100000001000000130000000100610100000000000000000002000000c800a925f28e454e44310100000000000000010000000000000045a13d0a9037e1667050793bd6dd00952c32ad5b50f7701e9888866e4306e807"
	want, err := hex.DecodeString(golden)
	if err != nil {
		t.Fatal(err)
	}
	events := []telemetry.Event{{TimestampUS: 1, Service: "a", DurationUS: 2, Status: 200}}
	for range 3 {
		got := snapshotEvents(t, events)
		if !bytes.Equal(got, want) {
			t.Fatalf("wire bytes:\n%x\nwant:\n%x", got, want)
		}
	}
	got, info := readEvents(t, want, 1)
	if !reflect.DeepEqual(got, events) || info != (Info{Events: 1, Blocks: 1}) {
		t.Fatalf("golden read = %+v, %+v", got, info)
	}
}

func TestRoundTripBoundaryValues(t *testing.T) {
	events := []telemetry.Event{
		{TimestampUS: 0, Service: "a", DurationUS: 0, Status: 100},
		{TimestampUS: 0, Service: "服务🚀", DurationUS: math.MaxUint32, Status: 599},
		{TimestampUS: math.MaxInt64, Service: strings.Repeat("é", 64), DurationUS: 1, Status: 200},
		{TimestampUS: math.MaxInt64, Service: "a", DurationUS: 42, Status: 500},
	}
	got, info := readEvents(t, snapshotEvents(t, events), len(events))
	if !reflect.DeepEqual(got, events) || info != (Info{Events: 4, Blocks: 1}) {
		t.Fatalf("round trip = %+v, %+v", got, info)
	}
}

func TestGeneratedProfiles(t *testing.T) {
	for _, profile := range []string{generator.Uniform, generator.Incident} {
		t.Run(profile, func(t *testing.T) {
			var source bytes.Buffer
			config := generator.Config{
				Events: blockRows + 17, Services: 7, Seed: 123,
				Start: time.Unix(1, 0), Interval: 8 * time.Microsecond,
				ErrorPercent: 20, Profile: profile,
			}
			if err := generator.Generate(context.Background(), &source, config); err != nil {
				t.Fatal(err)
			}
			var want []telemetry.Event
			err := telemetry.ReadJSONL(context.Background(), bytes.NewReader(source.Bytes()), func(event telemetry.Event) error {
				want = append(want, event)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var dst bytes.Buffer
			written, err := WriteJSONL(context.Background(), &dst, bytes.NewReader(source.Bytes()), int(config.Events))
			if err != nil {
				t.Fatal(err)
			}
			got, read := readEvents(t, dst.Bytes(), int(config.Events))
			if !reflect.DeepEqual(got, want) || written != read || read.Blocks != 2 {
				t.Fatalf("profile round trip mismatch; write=%+v read=%+v", written, read)
			}
			if !bytes.Equal(dst.Bytes(), snapshotEvents(t, want)) {
				t.Fatal("multi-service bytes are not deterministic")
			}
		})
	}
}

func TestBlocksAndServiceTransitions(t *testing.T) {
	events := make([]telemetry.Event, blockRows*2+1)
	for i := range events {
		name := fmt.Sprintf("service-%04d", i%blockRows)
		if i >= blockRows {
			name = fmt.Sprintf("service-%04d", blockRows-1-i%blockRows)
		}
		events[i] = telemetry.Event{TimestampUS: int64(i / 2), Service: name, Status: 200}
	}
	got, info := readEvents(t, snapshotEvents(t, events), len(events))
	if !reflect.DeepEqual(got, events) || info != (Info{Events: uint64(len(events)), Blocks: 3}) {
		t.Fatalf("cross-block round trip mismatch: %+v", info)
	}
}

func TestMaximumPayload(t *testing.T) {
	events := make([]telemetry.Event, blockRows)
	for i := range events {
		name := fmt.Sprintf("%04d", i) + strings.Repeat("x", telemetry.MaxServiceBytes-4)
		events[i] = telemetry.Event{TimestampUS: int64(i), Service: name, DurationUS: math.MaxUint32, Status: 599}
	}
	data := snapshotEvents(t, events)
	if got := binary.LittleEndian.Uint32(data[28:]); got != maxPayload {
		t.Fatalf("maximum payload = %d; want %d", got, maxPayload)
	}
	got, info := readEvents(t, data, blockRows)
	if !reflect.DeepEqual(got, events) || info != (Info{Events: blockRows, Blocks: 1}) {
		t.Fatal("maximum payload did not round trip")
	}
}

func TestEmpty(t *testing.T) {
	data := snapshotEvents(t, nil)
	if len(data) != 68 {
		t.Fatalf("empty length = %d", len(data))
	}
	got, info := readEvents(t, data, 1)
	if len(got) != 0 || info != (Info{}) {
		t.Fatalf("empty read = %v, %+v", got, info)
	}
}

func TestHeaderAndFramingRejections(t *testing.T) {
	base := snapshotEvents(t, []telemetry.Event{{Service: "a", Status: 200}})
	tests := []struct {
		name, want string
		change     func([]byte)
	}{
		{"magic", "magic", func(b []byte) { b[0] ^= 1 }},
		{"version", "version", func(b []byte) { b[8] = 2 }},
		{"zero version", "version", func(b []byte) { b[8] = 0 }},
		{"flags", "flags", func(b []byte) { b[10] = 1 }},
		{"reserved", "reserved", func(b []byte) { b[15] = 1 }},
		{"marker", "block 1", func(b []byte) { b[16] = '?' }},
		{"zero rows", "record count", func(b []byte) { binary.LittleEndian.PutUint32(b[20:], 0) }},
		{"oversize rows", "record count", func(b []byte) { binary.LittleEndian.PutUint32(b[20:], math.MaxUint32) }},
		{"zero names", "dictionary count", func(b []byte) { binary.LittleEndian.PutUint32(b[24:], 0) }},
		{"oversize names", "dictionary count", func(b []byte) { binary.LittleEndian.PutUint32(b[24:], math.MaxUint32) }},
		{"names exceed rows", "dictionary count", func(b []byte) { binary.LittleEndian.PutUint32(b[24:], 2) }},
		{"zero payload", "payload length", func(b []byte) { binary.LittleEndian.PutUint32(b[28:], 0) }},
		{"oversize payload", "payload length", func(b []byte) { binary.LittleEndian.PutUint32(b[28:], math.MaxUint32) }},
		{"payload impossible for counts", "payload length", func(b []byte) { binary.LittleEndian.PutUint32(b[28:], 147) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := bytes.Clone(base)
			test.change(data)
			// Omit the payload: rejection must happen before trying to read or
			// allocate based on any attacker-controlled block length.
			src := bytes.NewReader(data[:32])
			info, err := Read(context.Background(), src, 10, discardEvent)
			requireError(t, info, err, test.want)
		})
	}
}

func TestSemanticCorruptionWithValidChecksums(t *testing.T) {
	base := snapshotEvents(t, []telemetry.Event{
		{TimestampUS: 1, Service: "aa", Status: 100},
		{TimestampUS: 2, Service: "bb", Status: 599},
	})
	tests := []struct {
		name, want string
		change     func([]byte)
	}{
		{"empty name", "invalid name length", func(b []byte) { binary.LittleEndian.PutUint16(b[32:], 0) }},
		{"long name", "invalid name length", func(b []byte) { binary.LittleEndian.PutUint16(b[32:], 129) }},
		{"name beyond dictionary", "invalid name length", func(b []byte) { binary.LittleEndian.PutUint16(b[32:], 9) }},
		{"missing name length", "missing name length", func(b []byte) { binary.LittleEndian.PutUint16(b[32:], 5) }},
		{"invalid UTF8", "UTF-8", func(b []byte) { b[34] = 0xff }},
		{"leading whitespace", "whitespace", func(b []byte) { b[34] = ' ' }},
		{"trailing whitespace", "whitespace", func(b []byte) { b[35] = '\t' }},
		{"unicode whitespace", "whitespace", func(b []byte) { b[34], b[35] = 0xc2, 0xa0 }},
		{"duplicate service", "duplicate service", func(b []byte) { copy(b[38:40], "aa") }},
		{"unused dictionary bytes", "unexpected bytes", func(b []byte) { binary.LittleEndian.PutUint32(b[24:], 1) }},
		{"negative timestamp", "record 1: timestamp", func(b []byte) { binary.LittleEndian.PutUint64(b[40:], uint64(1)<<63) }},
		{"decreasing timestamp", "record 2: timestamp", func(b []byte) { binary.LittleEndian.PutUint64(b[48:], 0) }},
		{"invalid service ID", "record 1: service ID", func(b []byte) { binary.LittleEndian.PutUint16(b[56:], math.MaxUint16) }},
		{"low status", "record 1: status", func(b []byte) { binary.LittleEndian.PutUint16(b[68:], 99) }},
		{"high status", "record 2: status", func(b []byte) { binary.LittleEndian.PutUint16(b[70:], 600) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := bytes.Clone(base)
			test.change(data)
			repairFirstCRC(data)
			repairSHA(data)
			info, err := Read(context.Background(), bytes.NewReader(data), 2, discardEvent)
			requireError(t, info, err, test.want)
		})
	}
}

func TestTruncationEveryByteAndTrailingData(t *testing.T) {
	for _, events := range [][]telemetry.Event{nil, {{Service: "a", Status: 200}}} {
		data := snapshotEvents(t, events)
		for end := 0; end < len(data); end++ {
			info, err := Read(context.Background(), bytes.NewReader(data[:end]), 10, discardEvent)
			requireError(t, info, err, "")
		}
		for _, suffix := range [][]byte{{0}, []byte("END1"), data} {
			long := append(bytes.Clone(data), suffix...)
			info, err := Read(context.Background(), bytes.NewReader(long), 10, discardEvent)
			requireError(t, info, err, "trailing bytes")
		}
	}
}

func TestChecksumsAndTotals(t *testing.T) {
	base := snapshotEvents(t, []telemetry.Event{{TimestampUS: 1, Service: "a", Status: 200}})
	tests := []struct {
		name, want string
		change     func([]byte)
	}{
		{"payload CRC", "block 1: CRC32C", func(b []byte) { b[45] ^= 1 }},
		{"stored CRC", "block 1: CRC32C", func(b []byte) { b[51] ^= 1 }},
		{"framing CRC", "block 1: CRC32C", func(b []byte) {
			// Payload length still satisfies the count bounds.
			binary.LittleEndian.PutUint32(b[28:], 20)
		}},
		{"valid CRC wrong SHA", "SHA-256", func(b []byte) { b[45] ^= 1; repairFirstCRC(b) }},
		{"stored SHA", "SHA-256", func(b []byte) { b[len(b)-1] ^= 1 }},
		{"event total", "trailer totals", func(b []byte) { b[len(b)-48] ^= 1; repairSHA(b) }},
		{"block total", "trailer totals", func(b []byte) { b[len(b)-40] ^= 1; repairSHA(b) }},
		{"footer marker", "invalid framing marker", func(b []byte) { b[len(b)-52] ^= 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := bytes.Clone(base)
			test.change(data)
			info, err := Read(context.Background(), bytes.NewReader(data), 2, discardEvent)
			requireError(t, info, err, test.want)
		})
	}
}

func rawSnapshot(t testing.TB, groups ...[]telemetry.Event) []byte {
	t.Helper()
	empty := snapshotEvents(t, nil)
	data := bytes.Clone(empty[:headerBytes])
	var count uint64
	for _, group := range groups {
		block, err := encodeBlock(context.Background(), group)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, block...)
		count += uint64(len(group))
	}
	data = append(data, []byte(trailerMarker)...)
	data = binary.LittleEndian.AppendUint64(data, count)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(groups)))
	data = append(data, make([]byte, sha256.Size)...)
	repairSHA(data)
	return data
}

func TestGlobalTimestampOrderingAndReordering(t *testing.T) {
	a := []telemetry.Event{{TimestampUS: 2, Service: "a", Status: 200}}
	b := []telemetry.Event{{TimestampUS: 1, Service: "b", Status: 200}}
	info, err := Read(context.Background(), bytes.NewReader(rawSnapshot(t, a, b)), 2, discardEvent)
	requireError(t, info, err, "block 2: record 1: timestamp")
	b[0].TimestampUS = 2
	data := rawSnapshot(t, a, b)
	wantDigest := bytes.Clone(data[len(data)-sha256.Size:])
	reordered := rawSnapshot(t, b, a)
	copy(reordered[len(reordered)-sha256.Size:], wantDigest)
	info, err = Read(context.Background(), bytes.NewReader(reordered), 2, discardEvent)
	requireError(t, info, err, "SHA-256")
	got, read := readEvents(t, data, 2)
	if len(got) != 2 || read.Blocks != 2 {
		t.Fatal("equal timestamps across blocks must be valid")
	}
}

func TestMultiblockTruncationAndOmission(t *testing.T) {
	event := []telemetry.Event{{Service: "a", Status: 200}}
	data := rawSnapshot(t, event, event)
	blockLength := framingBytes + int(binary.LittleEndian.Uint32(data[28:])) + 4
	for _, start := range []int{headerBytes, headerBytes + blockLength} {
		for _, offset := range []int{0, 1, 4, 15, 16, blockLength - 4, blockLength - 1, blockLength} {
			info, err := Read(context.Background(), bytes.NewReader(data[:start+offset]), 2, discardEvent)
			requireError(t, info, err, "")
		}
	}
	omitted := append(bytes.Clone(data[:headerBytes]), data[headerBytes+blockLength:]...)
	info, err := Read(context.Background(), bytes.NewReader(omitted), 2, discardEvent)
	requireError(t, info, err, "trailer totals")
	// Even plausible replacement totals cannot hide removal without also
	// replacing the whole-file digest (this is integrity, not authentication).
	binary.LittleEndian.PutUint64(omitted[len(omitted)-48:], 1)
	binary.LittleEndian.PutUint64(omitted[len(omitted)-40:], 1)
	info, err = Read(context.Background(), bytes.NewReader(omitted), 2, discardEvent)
	requireError(t, info, err, "SHA-256")
}

func TestEventLimitsAndInvalidArguments(t *testing.T) {
	events := []telemetry.Event{{Service: "a", Status: 200}, {Service: "b", Status: 200}}
	data := snapshotEvents(t, events)
	for _, limit := range []int{-1, 0, 1} {
		var dst bytes.Buffer
		info, err := WriteJSONL(context.Background(), &dst, bytes.NewReader(jsonEvents(t, events)), limit)
		requireError(t, info, err, "")
		if limit <= 0 && dst.Len() != 0 {
			t.Fatal("invalid maxEvents wrote output")
		}
		visits := 0
		info, err = Read(context.Background(), bytes.NewReader(data), limit, func(telemetry.Event) error {
			visits++
			return nil
		})
		requireError(t, info, err, "")
		if visits != 0 {
			t.Fatal("oversized first block was visited")
		}
	}
	info, err := Read(context.Background(), bytes.NewReader(data), 2, nil)
	requireError(t, info, err, "visit")
	_, info = readEvents(t, data, 2)
	if info.Events != 2 {
		t.Fatal(info)
	}
	multi := rawSnapshot(t, events[:1], events[1:])
	info, err = Read(context.Background(), bytes.NewReader(multi), 1, discardEvent)
	requireError(t, info, err, "block 2: event limit")
}

func TestWriterRejectsInvalidJSONL(t *testing.T) {
	good := `{"timestamp_us":1,"service":"a","duration_us":0,"status":200}`
	tests := []string{
		"{", "null", good + good, good + "\n\n",
		`{"timestamp_us":-1,"service":"a","duration_us":0,"status":200}`,
		`{"timestamp_us":0,"service":"","duration_us":0,"status":200}`,
		`{"timestamp_us":0,"service":" a","duration_us":0,"status":200}`,
		`{"timestamp_us":0,"service":"a","duration_us":0,"status":99}`,
		`{"timestamp_us":0,"service":"a","duration_us":0,"status":600}`,
		`{"timestamp_us":0,"service":"a","duration_us":4294967296,"status":200}`,
		`{"timestamp_us":0,"service":"a","duration_us":0,"status":200,"extra":0}`,
		`{"timestamp_us":0,"timestamp_us":0,"service":"a","duration_us":0,"status":200}`,
		`{"timestamp_us":0,"service":"a","duration_us":null,"status":200}`,
		`{"timestamp_us":0,"service":"a","duration_us":0}`,
		good + "\n" + strings.Replace(good, `"timestamp_us":1`, `"timestamp_us":0`, 1),
		strings.Replace(good, `"a"`, `"`+strings.Repeat("a", 129)+`"`, 1),
		strings.Replace(good, `"a"`, "\"\xff\"", 1),
	}
	for i, input := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var dst bytes.Buffer
			info, err := WriteJSONL(context.Background(), &dst, strings.NewReader(input), 10)
			requireError(t, info, err, "read JSONL")
		})
	}
}

func TestGlobalServiceLimit(t *testing.T) {
	events := make([]telemetry.Event, maxServices+1)
	for i := range events {
		events[i] = telemetry.Event{Service: fmt.Sprintf("s%05d", i), Status: 200}
	}
	data := snapshotEvents(t, events[:maxServices])
	_, info := readEvents(t, data, maxServices)
	if info.Events != maxServices {
		t.Fatal(info)
	}
	var dst bytes.Buffer
	info, err := WriteJSONL(context.Background(), &dst, bytes.NewReader(jsonEvents(t, events)), len(events))
	requireError(t, info, err, "global service limit")
	var groups [][]telemetry.Event
	for start := 0; start < len(events); start += blockRows {
		groups = append(groups, events[start:min(start+blockRows, len(events))])
	}
	info, err = Read(context.Background(), bytes.NewReader(rawSnapshot(t, groups...)), len(events), discardEvent)
	requireError(t, info, err, "block 17: dictionary entry 1: global service limit")
}

type chunkReader struct {
	src io.Reader
	n   int
}

func (r chunkReader) Read(p []byte) (int, error) {
	return r.src.Read(p[:min(len(p), r.n)])
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type funcWriter func([]byte) (int, error)

func (w funcWriter) Write(p []byte) (int, error) { return w(p) }

type funcReader func([]byte) (int, error)

func (r funcReader) Read(p []byte) (int, error) { return r(p) }

type ownedBuffer struct {
	bytes.Buffer
	closed bool
}

func (b *ownedBuffer) Close() error {
	b.closed = true
	return nil
}

func TestStreamOwnershipAndDataWithEOF(t *testing.T) {
	events := []telemetry.Event{{Service: "a", Status: 200}}
	var source, dst ownedBuffer
	source.Write(jsonEvents(t, events))
	info, err := WriteJSONL(context.Background(), &dst, &source, 1)
	if err != nil || info.Events != 1 || source.closed || dst.closed {
		t.Fatalf("write ownership: %+v, %v; closed=%v/%v", info, err, source.closed, dst.closed)
	}
	data := bytes.Clone(dst.Bytes())
	info, err = Read(context.Background(), &dst, 1, discardEvent)
	if err != nil || info.Events != 1 || dst.closed {
		t.Fatalf("read ownership: %+v, %v; closed=%v", info, err, dst.closed)
	}
	r := bytes.NewReader(data)
	src := funcReader(func(p []byte) (int, error) {
		n, err := r.Read(p)
		if r.Len() == 0 {
			return n, io.EOF
		}
		return n, err
	})
	info, err = Read(context.Background(), src, 1, discardEvent)
	if err != nil || info.Events != 1 {
		t.Fatalf("final read with data and EOF: %+v, %v", info, err)
	}
}

func TestChunkedReadsAndReaderErrors(t *testing.T) {
	events := []telemetry.Event{{TimestampUS: 1, Service: "服务", Status: 200}}
	source := jsonEvents(t, events)
	data := snapshotEvents(t, events)
	for _, chunk := range []int{1, 3, 7, 17} {
		var dst bytes.Buffer
		written, err := WriteJSONL(context.Background(), &dst, chunkReader{bytes.NewReader(source), chunk}, 1)
		if err != nil || written.Events != 1 || !bytes.Equal(dst.Bytes(), data) {
			t.Fatalf("chunk %d write = %+v, %v", chunk, written, err)
		}
		read, err := Read(context.Background(), chunkReader{bytes.NewReader(data), chunk}, 1, discardEvent)
		if err != nil || read != written {
			t.Fatalf("chunk %d read = %+v, %v", chunk, read, err)
		}
	}
	sentinel := errors.New("source failed")
	for at := 0; at <= len(data); at++ {
		src := io.MultiReader(bytes.NewReader(data[:at]), errorReader{sentinel})
		info, err := Read(context.Background(), src, 1, discardEvent)
		requireError(t, info, err, "source failed")
		if !errors.Is(err, sentinel) {
			t.Fatal("reader error lost wrapping")
		}
	}
	info, err := WriteJSONL(context.Background(), io.Discard, io.MultiReader(bytes.NewReader(source), errorReader{sentinel}), 1)
	requireError(t, info, err, "source failed")
	if !errors.Is(err, sentinel) {
		t.Fatal("JSONL reader error lost wrapping")
	}
}

func TestShortWritesAndWriteErrors(t *testing.T) {
	source := jsonEvents(t, []telemetry.Event{{Service: "a", Status: 200}})
	sentinel := errors.New("destination failed")
	// One event emits header, block, trailer totals, then final digest.
	for failAt := 1; failAt <= 4; failAt++ {
		for _, short := range []bool{false, true} {
			calls := 0
			dst := funcWriter(func(p []byte) (int, error) {
				calls++
				if calls == failAt {
					if short {
						return len(p) - 1, nil
					}
					return len(p) / 2, sentinel
				}
				return len(p), nil
			})
			info, err := WriteJSONL(context.Background(), dst, bytes.NewReader(source), 1)
			requireError(t, info, err, "")
			want := sentinel
			if short {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) {
				t.Fatalf("write %d error = %v; want %v", failAt, err, want)
			}
		}
	}
}

func TestCallbackFailuresAndCancellation(t *testing.T) {
	events := []telemetry.Event{{Service: "a", Status: 200}, {Service: "b", Status: 200}}
	data := snapshotEvents(t, events)
	sentinel := errors.New("callback failed")
	visits := 0
	info, err := Read(context.Background(), bytes.NewReader(data), 2, func(telemetry.Event) error {
		visits++
		if visits == 2 {
			return sentinel
		}
		return nil
	})
	requireError(t, info, err, "block 1: record 2 callback")
	if !errors.Is(err, sentinel) || visits != 2 {
		t.Fatalf("callback error = %v; visits = %d", err, visits)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	info, err = Read(ctx, bytes.NewReader(data), 2, discardEvent)
	requireError(t, info, err, "context canceled")
	var dst bytes.Buffer
	info, err = WriteJSONL(ctx, &dst, bytes.NewReader(jsonEvents(t, events)), 2)
	requireError(t, info, err, "context canceled")
	if dst.Len() != 0 {
		t.Fatal("pre-canceled writer wrote bytes")
	}
	for cancelAt := 1; cancelAt <= 2; cancelAt++ {
		ctx, cancel := context.WithCancel(context.Background())
		visits := 0
		info, err := Read(ctx, bytes.NewReader(data), 2, func(telemetry.Event) error {
			visits++
			if visits == cancelAt {
				cancel()
			}
			return nil
		})
		cancel()
		requireError(t, info, err, "context canceled")
		if !errors.Is(err, context.Canceled) || visits != cancelAt {
			t.Fatalf("cancel callback error = %v; visits = %d", err, visits)
		}
	}
}

func TestCancellationDuringIO(t *testing.T) {
	events := []telemetry.Event{{Service: "a", Status: 200}}
	data := snapshotEvents(t, events)
	source := jsonEvents(t, events)
	for _, input := range []struct {
		name string
		data []byte
		read func(context.Context, io.Reader) (Info, error)
	}{
		{"snapshot", data, func(ctx context.Context, r io.Reader) (Info, error) { return Read(ctx, r, 1, discardEvent) }},
		{"JSONL", source, func(ctx context.Context, r io.Reader) (Info, error) {
			return WriteJSONL(ctx, io.Discard, r, 1)
		}},
	} {
		t.Run(input.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := bytes.NewReader(input.data)
			reader := funcReader(func(p []byte) (int, error) {
				n, err := r.Read(p)
				cancel()
				return n, err
			})
			info, err := input.read(ctx, reader)
			requireError(t, info, err, "context canceled")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	for cancelAt := 1; cancelAt <= 4; cancelAt++ {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		writer := funcWriter(func(p []byte) (int, error) {
			calls++
			if calls == cancelAt {
				cancel()
			}
			return len(p), nil
		})
		info, err := WriteJSONL(ctx, writer, bytes.NewReader(source), 1)
		cancel()
		requireError(t, info, err, "context canceled")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestFailureAfterFullBlock(t *testing.T) {
	events := make([]telemetry.Event, blockRows)
	for i := range events {
		events[i] = telemetry.Event{Service: "a", Status: 200}
	}
	source := append(jsonEvents(t, events), []byte("{broken}\n")...)
	var dst bytes.Buffer
	info, err := WriteJSONL(context.Background(), &dst, bytes.NewReader(source), blockRows+1)
	requireError(t, info, err, "read JSONL")
	if dst.Len() <= headerBytes {
		t.Fatal("test did not exercise a failure after an emitted block")
	}
	source = append(jsonEvents(t, events), jsonEvents(t, events[:1])...)
	info, err = WriteJSONL(context.Background(), io.Discard, bytes.NewReader(source), blockRows)
	requireError(t, info, err, "block 2 record 1: event limit")
	bad := snapshotEvents(t, events)
	bad[len(bad)-1] ^= 1
	visits := 0
	info, err = Read(context.Background(), bytes.NewReader(bad), blockRows, func(telemetry.Event) error {
		visits++
		return nil
	})
	requireError(t, info, err, "SHA-256")
	if visits != blockRows {
		t.Fatal("test did not exercise final checksum failure after visits")
	}
}

func FuzzRead(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("not a snapshot"))
	f.Add(snapshotEvents(f, nil))
	f.Add(snapshotEvents(f, []telemetry.Event{{Service: "a", Status: 100}, {TimestampUS: math.MaxInt64, Service: "服务", DurationUS: math.MaxUint32, Status: 599}}))
	invalid := snapshotEvents(f, []telemetry.Event{{Service: "a", Status: 200}})
	invalid[28], invalid[29], invalid[30], invalid[31] = 0xff, 0xff, 0xff, 0xff
	f.Add(invalid)
	f.Fuzz(func(t *testing.T, data []byte) {
		const limit = 32
		visits := 0
		var previous int64
		info, err := Read(context.Background(), bytes.NewReader(data), limit, func(event telemetry.Event) error {
			visits++
			if visits > limit || event.TimestampUS < previous {
				t.Fatal("reader violated event count or order")
			}
			if err := event.Validate(); err != nil {
				t.Fatalf("reader visited invalid event: %v", err)
			}
			previous = event.TimestampUS
			return nil
		})
		if err != nil && info != (Info{}) {
			t.Fatalf("nonzero info on error: %+v, %v", info, err)
		}
		if err == nil && (info.Events != uint64(visits) || info.Blocks > info.Events) {
			t.Fatalf("inconsistent successful info: %+v, visits %d", info, visits)
		}
	})
}
