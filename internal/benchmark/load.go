package benchmark

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/measure"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

type contextReader struct {
	ctx   context.Context
	input io.Reader
	bytes int64
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.input.Read(buffer)
	r.bytes += int64(n)
	if contextErr := r.ctx.Err(); contextErr != nil {
		return n, contextErr
	}
	return n, err
}

func loadFile(ctx context.Context, path string, engine query.Engine, maxEvents int) (*query.Dataset, Load, error) {
	if err := ctx.Err(); err != nil {
		return nil, Load{}, err
	}
	start := time.Now()
	before, err := os.Stat(path)
	if err != nil {
		return nil, Load{}, fmt.Errorf("stat input: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, Load{}, fmt.Errorf("input must be a regular JSONL file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, Load{}, fmt.Errorf("open input: %w", err)
	}
	hash := sha256.New()
	reader := &contextReader{ctx: ctx, input: io.TeeReader(file, hash)}
	data, loadErr := query.Load(ctx, reader, engine, maxEvents)
	after, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(loadErr, statErr, closeErr, ctx.Err()); err != nil {
		return nil, Load{}, fmt.Errorf("load input: %w", err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) || reader.bytes != after.Size() {
		return nil, Load{}, fmt.Errorf("input changed while loading")
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	return data, Load{MS: measure.Milliseconds(time.Since(start)), SHA256: digest, Bytes: reader.bytes}, nil
}
