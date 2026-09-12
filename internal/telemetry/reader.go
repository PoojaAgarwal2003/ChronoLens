package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	MaxRecordBytes  = 4096
	MaxServiceBytes = 128
)

func ValidateService(service string) error {
	if service == "" || len(service) > MaxServiceBytes || strings.TrimSpace(service) != service {
		return fmt.Errorf("service must contain 1-%d UTF-8 bytes without surrounding whitespace", MaxServiceBytes)
	}
	if !utf8.ValidString(service) {
		return fmt.Errorf("service must be valid UTF-8")
	}
	return nil
}

func (e Event) Validate() error {
	if e.TimestampUS < 0 {
		return fmt.Errorf("timestamp_us must be nonnegative")
	}
	if err := ValidateService(e.Service); err != nil {
		return err
	}
	if e.Status < 100 || e.Status > 599 {
		return fmt.Errorf("status must be between 100 and 599")
	}
	return nil
}

// ReadJSONL visits validated events in timestamp order. Equal timestamps are
// allowed. A failure may follow earlier visits; callers must discard partial
// datasets. Cancellation is checked between records, not during a blocked Read.
func ReadJSONL(ctx context.Context, input io.Reader, visit func(Event) error) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, MaxRecordBytes+2), MaxRecordBytes+2)
	var previous int64
	lineNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !scanner.Scan() {
			break
		}
		lineNumber++
		line := scanner.Bytes()
		if len(line) > MaxRecordBytes {
			return fmt.Errorf("line %d: record exceeds %d bytes", lineNumber, MaxRecordBytes)
		}
		event, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if lineNumber > 1 && event.TimestampUS < previous {
			return fmt.Errorf("line %d: timestamp_us is less than the previous timestamp", lineNumber)
		}
		if err := visit(event); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		previous = event.TimestampUS
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read line %d (maximum record size %d bytes): %w", lineNumber+1, MaxRecordBytes, err)
	}
	return ctx.Err()
}

func decodeEvent(line []byte) (Event, error) {
	if !utf8.Valid(line) {
		return Event{}, fmt.Errorf("record must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	token, err := decoder.Token()
	if err != nil {
		return Event{}, fmt.Errorf("decode object: %w", err)
	}
	if token != json.Delim('{') {
		return Event{}, fmt.Errorf("record must be a JSON object")
	}
	var event Event
	var seen uint8
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Event{}, fmt.Errorf("decode field name: %w", err)
		}
		var bit uint8
		var target any
		switch token {
		case "timestamp_us":
			bit, target = 1, &event.TimestampUS
		case "service":
			bit, target = 2, &event.Service
		case "duration_us":
			bit, target = 4, &event.DurationUS
		case "status":
			bit, target = 8, &event.Status
		default:
			return Event{}, fmt.Errorf("unknown field %q", token)
		}
		if seen&bit != 0 {
			return Event{}, fmt.Errorf("duplicate field %q", token)
		}
		seen |= bit
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return Event{}, fmt.Errorf("decode %s: %w", token, err)
		}
		if bytes.Equal(raw, []byte("null")) {
			return Event{}, fmt.Errorf("field %s must not be null", token)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return Event{}, fmt.Errorf("decode %s: %w", token, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return Event{}, fmt.Errorf("close object: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Event{}, fmt.Errorf("record must contain exactly one JSON object")
	}
	if seen != 15 {
		return Event{}, fmt.Errorf("record requires timestamp_us, service, duration_us, and status")
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}
