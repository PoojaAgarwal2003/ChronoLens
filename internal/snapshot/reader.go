package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

// Read validates a version 1 snapshot and visits its events in wire order. Info
// is zero on any error. Visits can precede a later checksum, trailer, or callback
// failure: discard all partial results. The caller's stream is never closed.
func Read(ctx context.Context, src io.Reader, maxEvents int, visit func(telemetry.Event) error) (Info, error) {
	if err := checkLimit(maxEvents); err != nil {
		return Info{}, err
	}
	if visit == nil {
		return Info{}, fmt.Errorf("visit must not be nil")
	}
	digest := sha256.New()
	var header [headerBytes]byte
	if err := readBytes(ctx, src, digest, header[:]); err != nil {
		return Info{}, fmt.Errorf("read header: %w", err)
	}
	if string(header[:8]) != Magic {
		return Info{}, fmt.Errorf("invalid snapshot magic")
	}
	if v := binary.LittleEndian.Uint16(header[8:]); v != version {
		return Info{}, fmt.Errorf("unsupported snapshot version %d", v)
	}
	if binary.LittleEndian.Uint16(header[10:]) != 0 || binary.LittleEndian.Uint32(header[12:]) != 0 {
		return Info{}, fmt.Errorf("unsupported snapshot flags or reserved bits")
	}

	var info Info
	var previous int64
	services := make(map[string]struct{})
	for {
		var framing [framingBytes]byte
		if err := readBytes(ctx, src, digest, framing[:4]); err != nil {
			return Info{}, fmt.Errorf("block %d framing or mandatory trailer: %w", info.Blocks+1, err)
		}
		switch string(framing[:4]) {
		case trailerMarker:
			var totals [16]byte
			if err := readBytes(ctx, src, digest, totals[:]); err != nil {
				return Info{}, fmt.Errorf("read trailer totals: %w", err)
			}
			if binary.LittleEndian.Uint64(totals[:8]) != info.Events || binary.LittleEndian.Uint64(totals[8:]) != info.Blocks {
				return Info{}, fmt.Errorf("trailer totals do not match decoded events and blocks")
			}
			var checksum [sha256.Size]byte
			if err := readBytes(ctx, src, nil, checksum[:]); err != nil {
				return Info{}, fmt.Errorf("read file checksum: %w", err)
			}
			if !bytes.Equal(checksum[:], digest.Sum(nil)) {
				return Info{}, fmt.Errorf("file SHA-256 checksum mismatch")
			}
			var extra [1]byte
			err := readBytes(ctx, src, nil, extra[:])
			if err == nil {
				return Info{}, fmt.Errorf("trailing bytes after snapshot")
			}
			if err != io.EOF {
				return Info{}, fmt.Errorf("read end of snapshot: %w", err)
			}
			return info, nil
		case blockMarker:
		default:
			return Info{}, fmt.Errorf("block %d: invalid framing marker", info.Blocks+1)
		}
		if err := readBytes(ctx, src, digest, framing[4:]); err != nil {
			return Info{}, fmt.Errorf("block %d framing: %w", info.Blocks+1, err)
		}
		rows := binary.LittleEndian.Uint32(framing[4:])
		names := binary.LittleEndian.Uint32(framing[8:])
		length := binary.LittleEndian.Uint32(framing[12:])
		if rows == 0 || rows > blockRows {
			return Info{}, fmt.Errorf("block %d: record count %d outside 1..%d", info.Blocks+1, rows, blockRows)
		}
		if names == 0 || names > rows {
			return Info{}, fmt.Errorf("block %d: dictionary count %d outside 1..%d", info.Blocks+1, names, rows)
		}
		// Counts are bounded before multiplication or conversion to int.
		minimum := rows*columnBytes + names*3
		maximum := rows*columnBytes + names*(2+telemetry.MaxServiceBytes)
		if length < minimum || length > maximum || length > maxPayload {
			return Info{}, fmt.Errorf("block %d: payload length %d outside %d..%d", info.Blocks+1, length, minimum, maximum)
		}
		// Subtraction avoids overflow even for an adversarial file's totals.
		if uint64(rows) > uint64(maxEvents)-info.Events {
			return Info{}, fmt.Errorf("block %d: event limit %d exceeded", info.Blocks+1, maxEvents)
		}
		payload := make([]byte, int(length))
		if err := readBytes(ctx, src, digest, payload); err != nil {
			return Info{}, fmt.Errorf("block %d payload: %w", info.Blocks+1, err)
		}
		var checksum [4]byte
		if err := readBytes(ctx, src, digest, checksum[:]); err != nil {
			return Info{}, fmt.Errorf("block %d checksum: %w", info.Blocks+1, err)
		}
		crc := crc32.Update(0, castagnoli, framing[:])
		crc = crc32.Update(crc, castagnoli, payload)
		if binary.LittleEndian.Uint32(checksum[:]) != crc {
			return Info{}, fmt.Errorf("block %d: CRC32C checksum mismatch", info.Blocks+1)
		}
		if err := decodeBlock(ctx, payload, int(rows), int(names), services, &previous, visit); err != nil {
			return Info{}, fmt.Errorf("block %d: %w", info.Blocks+1, err)
		}
		info.Events += uint64(rows)
		info.Blocks++
	}
}

func decodeBlock(ctx context.Context, payload []byte, rows, names int, services map[string]struct{}, previous *int64, visit func(telemetry.Event) error) error {
	// Only the prefix can contain dictionary entries; columns have a fixed size.
	dictionaryEnd := len(payload) - rows*columnBytes
	dictionary := make([]string, 0, names)
	seen := make(map[string]struct{}, names)
	offset := 0
	for i := 0; i < names; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if dictionaryEnd-offset < 2 {
			return fmt.Errorf("dictionary entry %d: missing name length", i+1)
		}
		length := int(binary.LittleEndian.Uint16(payload[offset:]))
		offset += 2
		if length == 0 || length > telemetry.MaxServiceBytes || length > dictionaryEnd-offset {
			return fmt.Errorf("dictionary entry %d: invalid name length %d", i+1, length)
		}
		name := string(payload[offset : offset+length])
		offset += length
		if err := telemetry.ValidateService(name); err != nil {
			return fmt.Errorf("dictionary entry %d: %w", i+1, err)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("dictionary entry %d: duplicate service %q", i+1, name)
		}
		seen[name] = struct{}{}
		if _, exists := services[name]; !exists {
			if len(services) >= maxServices {
				return fmt.Errorf("dictionary entry %d: global service limit %d exceeded", i+1, maxServices)
			}
			services[name] = struct{}{}
		}
		dictionary = append(dictionary, name)
	}
	if offset != dictionaryEnd {
		return fmt.Errorf("dictionary has %d unexpected bytes", dictionaryEnd-offset)
	}
	columns := payload[dictionaryEnd:]
	for i := 0; i < rows; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("record %d: %w", i+1, err)
		}
		timestamp := int64(binary.LittleEndian.Uint64(columns[i*8:]))
		id := int(binary.LittleEndian.Uint16(columns[rows*8+i*2:]))
		duration := binary.LittleEndian.Uint32(columns[rows*10+i*4:])
		status := binary.LittleEndian.Uint16(columns[rows*14+i*2:])
		if id >= len(dictionary) {
			return fmt.Errorf("record %d: service ID %d outside dictionary", i+1, id)
		}
		if timestamp < 0 || timestamp < *previous {
			return fmt.Errorf("record %d: timestamp must be nonnegative and nondecreasing", i+1)
		}
		if status < 100 || status > 599 {
			return fmt.Errorf("record %d: status must be between 100 and 599", i+1)
		}
		event := telemetry.Event{TimestampUS: timestamp, Service: dictionary[id], DurationUS: duration, Status: status}
		if err := visit(event); err != nil {
			return fmt.Errorf("record %d callback: %w", i+1, err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("record %d: %w", i+1, err)
		}
		*previous = timestamp
	}
	return nil
}
