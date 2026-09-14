package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/generator"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("generator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config generator.Config
	flags.Int64Var(&config.Events, "events", 100000, "number of events (greater than zero)")
	flags.IntVar(&config.Services, "services", 16, "number of distinct service names (1-1024)")
	flags.Int64Var(&config.Seed, "seed", 42, "deterministic random seed")
	flags.StringVar(&config.Profile, "profile", generator.Uniform, "synthetic workload: uniform or incident")
	flags.DurationVar(&config.Interval, "interval", time.Millisecond, "spacing between timestamps (whole microseconds)")
	flags.IntVar(&config.ErrorPercent, "error-percent", 5, "baseline status 500 probability (0-100); congested incident requests use at least 80")
	start := flags.String("start", "2026-01-01T00:00:00Z", "first event time in RFC3339 format (microsecond precision)")
	output := flags.String("output", "data/events.jsonl", "new output file, or - for stdout; existing files are never overwritten")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: generator [flags]\n\nGenerate ordered, reproducible synthetic telemetry as JSONL.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "generator: unexpected positional arguments")
		return 2
	}
	var err error
	config.Start, err = parseStart(*start)
	if err != nil {
		fmt.Fprintf(stderr, "generator: invalid start: %v\n", err)
		return 2
	}
	if err := config.Validate(); err != nil {
		fmt.Fprintf(stderr, "generator: %v\n", err)
		return 2
	}
	if *output == "" {
		fmt.Fprintln(stderr, "generator: output must be a file path or -")
		return 2
	}
	if err := writeDataset(ctx, config, *output, stdout); err != nil {
		fmt.Fprintf(stderr, "generator: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "Generated %d events (seed=%d).\n", config.Events, config.Seed)
	return 0
}

func parseStart(value string) (time.Time, error) {
	start, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	// time.Parse truncates fractions beyond nanoseconds; reject precision loss
	// before that truncation can hide a non-microsecond input.
	if dot := strings.IndexAny(value, ".,"); dot >= 0 {
		fraction := value[dot+1:]
		for i := 0; i < len(fraction) && fraction[i] >= '0' && fraction[i] <= '9'; i++ {
			if i >= 6 && fraction[i] != '0' {
				return time.Time{}, errors.New("start must have microsecond precision")
			}
		}
	}
	return start, nil
}

func writeDataset(ctx context.Context, config generator.Config, output string, stdout io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if output == "-" {
		writer := bufio.NewWriterSize(stdout, 256*1024)
		if err := generator.Generate(ctx, writer, config); err != nil {
			return err
		}
		return writer.Flush()
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create output file (choose a new path if it exists): %w", err)
	}
	writer := bufio.NewWriterSize(file, 256*1024)
	err = generator.Generate(ctx, writer, config)
	if err == nil {
		err = writer.Flush()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		// Only remove the file this invocation exclusively created.
		return errors.Join(fmt.Errorf("write dataset: %w", err), os.Remove(output))
	}
	return nil
}
