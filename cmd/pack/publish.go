package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/snapshot"
)

type report struct {
	Input         string   `json:"input"`
	Output        string   `json:"output"`
	Events        uint64   `json:"events"`
	Blocks        uint64   `json:"blocks"`
	SourceSHA256  string   `json:"source_sha256"`
	SourceBytes   int64    `json:"source_bytes"`
	SnapshotBytes int64    `json:"snapshot_bytes"`
	PackMS        *float64 `json:"pack_ms"`
	TimingNote    string   `json:"timing_note,omitempty"`
}

type countingReader struct {
	io.Reader
	bytes int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytes += int64(n)
	return n, err
}

// Publishing a same-directory hard link creates the destination atomically
// without replacing a concurrent writer's file. Unsupported filesystems fail
// explicitly rather than falling back to an overwriting rename.
func pack(ctx context.Context, input, output string, maxEvents int, publish func(string, string) error) (result report, err error) {
	if err := ctx.Err(); err != nil {
		return report{}, err
	}
	if _, err := os.Lstat(output); err == nil {
		return report{}, fmt.Errorf("output already exists; choose a new path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return report{}, fmt.Errorf("check output: %w", err)
	}
	source, err := os.Open(input)
	if err != nil {
		return report{}, fmt.Errorf("open input: %w", err)
	}
	sourceOpen := true
	defer func() {
		if sourceOpen {
			err = errors.Join(err, source.Close())
		}
		if err != nil {
			result = report{}
		}
	}()
	before, err := source.Stat()
	if err != nil {
		return report{}, fmt.Errorf("stat input: %w", err)
	}
	if !before.Mode().IsRegular() {
		return report{}, fmt.Errorf("input must be a regular JSONL file")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return report{}, fmt.Errorf("create output directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(output), ".chronolens-pack-*.tmp")
	if err != nil {
		return report{}, fmt.Errorf("create private snapshot: %w", err)
	}
	open := true
	defer func() {
		if open {
			err = errors.Join(err, temp.Close())
		}
		if cleanupErr := os.Remove(temp.Name()); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove private snapshot (published output, if any, is preserved): %w", cleanupErr))
		}
		if err != nil {
			result = report{}
		}
	}()
	hash := sha256.New()
	reader := &countingReader{Reader: io.TeeReader(source, hash)}
	writer := bufio.NewWriterSize(temp, 256*1024)
	info, err := snapshot.WriteJSONL(ctx, writer, reader, maxEvents)
	if err != nil {
		return report{}, fmt.Errorf("encode snapshot: %w", err)
	}
	after, err := source.Stat()
	if err != nil {
		return report{}, fmt.Errorf("stat input after reading: %w", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || reader.bytes != after.Size() {
		return report{}, fmt.Errorf("input changed during conversion")
	}
	sourceCloseErr := source.Close()
	sourceOpen = false
	if sourceCloseErr != nil {
		return report{}, fmt.Errorf("close input: %w", sourceCloseErr)
	}
	if err := errors.Join(writer.Flush(), ctx.Err()); err != nil {
		return report{}, fmt.Errorf("flush snapshot: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return report{}, fmt.Errorf("sync snapshot: %w", err)
	}
	stat, err := temp.Stat()
	if err != nil {
		return report{}, fmt.Errorf("stat snapshot: %w", err)
	}
	closeErr := temp.Close()
	open = false
	if closeErr != nil {
		return report{}, fmt.Errorf("close snapshot: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return report{}, err
	}
	if err := publish(temp.Name(), output); err != nil {
		return report{}, fmt.Errorf("publish snapshot without replacement (filesystem must support hard links): %w", err)
	}
	return report{
		Input: filepath.Base(input), Output: filepath.Base(output),
		Events: info.Events, Blocks: info.Blocks,
		SourceSHA256: fmt.Sprintf("%x", hash.Sum(nil)), SourceBytes: reader.bytes, SnapshotBytes: stat.Size(),
	}, nil
}
