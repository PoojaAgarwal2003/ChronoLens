// Package snapshot implements the version 1 immutable, block-columnar telemetry
// format. All integer fields are little endian; no padding or timestamps are
// added. Blocks contain at most 4096 records.
//
// The exact wire layout is:
//
//   - Header (16 bytes): "CHRONOSN" at offset 0, uint16 version (1) at 8,
//     uint16 flags (0) at 10, uint32 reserved (0) at 12.
//   - Block framing (16 bytes): "BLK1" at 0, uint32 record count at 4,
//     uint32 dictionary count at 8, uint32 payload byte length at 12.
//   - Block payload: dictionary entries in ID order, each uint16 byte length
//     followed by UTF-8 bytes; then contiguous timestamp int64, service ID
//     uint16, duration uint32, and status uint16 columns, in that order.
//     IDs are zero-based. Writers assign IDs in first-occurrence order.
//   - Block checksum (4 bytes): CRC32C (Castagnoli) over framing and payload.
//   - Mandatory trailer (52 bytes): "END1" at 0, uint64 total record count at
//     4, uint64 total block count at 12, SHA-256 at 20. The SHA-256 covers all
//     preceding file bytes, including the trailer marker and both totals, but
//     not the digest itself. No trailing bytes are allowed.
//
// Empty snapshots consist of just the header and trailer. Each nonempty block
// has 1..4096 records and 1..record-count dictionary entries; service names obey
// telemetry.ValidateService. Dictionary names must be unique within a block.
// Timestamps must be nonnegative and globally nondecreasing; ties are valid.
//
// Reads and writes are streaming, with bounded block allocations. At most 65536
// distinct services are allowed globally, matching the query loader's schema
// limit without depending on the query package. A positive maxEvents is required.
//
// On failure, either operation can have produced partial writes or visits,
// including visits before the final file integrity check. Consumers must discard
// partial output and must not publish it before success. Errors return zero
// Info. Caller-owned streams are never closed. Context cancellation is checked
// at record/block boundaries and before and after I/O; it cannot interrupt a
// caller's blocked Read, Write, or callback.
package snapshot

import (
	"context"
	"fmt"
	"hash"
	"hash/crc32"
	"io"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

const (
	// Magic identifies the format family; the header separately carries its version.
	Magic = "CHRONOSN"

	version       = 1
	blockRows     = 4096
	maxServices   = 65536
	headerBytes   = 16
	framingBytes  = 16
	columnBytes   = 16 // 8 timestamp + 2 service ID + 4 duration + 2 status.
	maxPayload    = blockRows * (2 + telemetry.MaxServiceBytes + columnBytes)
	blockMarker   = "BLK1"
	trailerMarker = "END1"
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Info describes a completely written or validated snapshot.
type Info struct {
	Events uint64
	Blocks uint64
}

// contextReader checks cancellation around each underlying read, including
// reads performed internally by io.ReadFull or telemetry's buffered scanner.
type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

func readBytes(ctx context.Context, src io.Reader, digest hash.Hash, p []byte) error {
	_, err := io.ReadFull(contextReader{ctx, src}, p)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return err
	}
	if digest != nil {
		_, _ = digest.Write(p) // Standard-library hash writes cannot fail.
	}
	return nil
}

func writeBytes(ctx context.Context, dst io.Writer, digest hash.Hash, p []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := dst.Write(p)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return err
	}
	if n != len(p) {
		return io.ErrShortWrite
	}
	if digest != nil {
		_, _ = digest.Write(p)
	}
	return nil
}

func checkLimit(maxEvents int) error {
	if maxEvents <= 0 {
		return fmt.Errorf("maxEvents must be positive")
	}
	return nil
}
