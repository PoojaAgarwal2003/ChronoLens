package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

// WriteJSONL validates strict, timestamp-ordered JSONL and writes a deterministic
// version 1 snapshot. On error, Info is zero and dst may contain partial output;
// discard it. Neither stream is closed.
func WriteJSONL(ctx context.Context, dst io.Writer, src io.Reader, maxEvents int) (Info, error) {
	if err := checkLimit(maxEvents); err != nil {
		return Info{}, err
	}
	var header [headerBytes]byte
	copy(header[:], Magic)
	binary.LittleEndian.PutUint16(header[8:], version)
	digest := sha256.New()
	if err := writeBytes(ctx, dst, digest, header[:]); err != nil {
		return Info{}, fmt.Errorf("write header: %w", err)
	}

	var info Info
	events := make([]telemetry.Event, 0, blockRows)
	services := make(map[string]struct{})
	flush := func() error {
		if len(events) == 0 {
			return ctx.Err()
		}
		block, err := encodeBlock(ctx, events)
		if err != nil {
			return fmt.Errorf("block %d: %w", info.Blocks+1, err)
		}
		if err := writeBytes(ctx, dst, digest, block); err != nil {
			return fmt.Errorf("write block %d: %w", info.Blocks+1, err)
		}
		info.Blocks++
		clear(events)
		events = events[:0]
		return nil
	}
	err := telemetry.ReadJSONL(ctx, contextReader{ctx, src}, func(event telemetry.Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.Events >= uint64(maxEvents) {
			return fmt.Errorf("block %d record %d: event limit %d exceeded", info.Blocks+1, len(events)+1, maxEvents)
		}
		if _, exists := services[event.Service]; !exists {
			if len(services) >= maxServices {
				return fmt.Errorf("block %d record %d: global service limit %d exceeded", info.Blocks+1, len(events)+1, maxServices)
			}
			services[event.Service] = struct{}{}
		}
		events = append(events, event)
		info.Events++
		if len(events) == blockRows {
			return flush()
		}
		return nil
	})
	if err != nil {
		return Info{}, fmt.Errorf("read JSONL: %w", err)
	}
	if err := flush(); err != nil {
		return Info{}, err
	}
	var trailer [20]byte
	copy(trailer[:], trailerMarker)
	binary.LittleEndian.PutUint64(trailer[4:], info.Events)
	binary.LittleEndian.PutUint64(trailer[12:], info.Blocks)
	if err := writeBytes(ctx, dst, digest, trailer[:]); err != nil {
		return Info{}, fmt.Errorf("write trailer: %w", err)
	}
	if err := writeBytes(ctx, dst, nil, digest.Sum(nil)); err != nil {
		return Info{}, fmt.Errorf("write file checksum: %w", err)
	}
	return info, nil
}

func encodeBlock(ctx context.Context, events []telemetry.Event) ([]byte, error) {
	ids := make(map[string]uint16)
	names := make([]string, 0)
	for i, event := range events {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("record %d: %w", i+1, err)
		}
		if _, exists := ids[event.Service]; !exists {
			ids[event.Service] = uint16(len(names))
			names = append(names, event.Service)
		}
	}
	payloadBytes := len(events) * columnBytes
	for _, name := range names {
		payloadBytes += 2 + len(name)
	}
	block := make([]byte, framingBytes, framingBytes+payloadBytes+4)
	copy(block, blockMarker)
	binary.LittleEndian.PutUint32(block[4:], uint32(len(events)))
	binary.LittleEndian.PutUint32(block[8:], uint32(len(names)))
	binary.LittleEndian.PutUint32(block[12:], uint32(payloadBytes))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		block = binary.LittleEndian.AppendUint16(block, uint16(len(name)))
		block = append(block, name...)
	}
	columnStart := len(block)
	block = block[:columnStart+len(events)*columnBytes]
	n := len(events)
	for i, event := range events {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("record %d: %w", i+1, err)
		}
		binary.LittleEndian.PutUint64(block[columnStart+i*8:], uint64(event.TimestampUS))
		binary.LittleEndian.PutUint16(block[columnStart+n*8+i*2:], ids[event.Service])
		binary.LittleEndian.PutUint32(block[columnStart+n*10+i*4:], event.DurationUS)
		binary.LittleEndian.PutUint16(block[columnStart+n*14+i*2:], event.Status)
	}
	return binary.LittleEndian.AppendUint32(block, crc32.Checksum(block, castagnoli)), nil
}
