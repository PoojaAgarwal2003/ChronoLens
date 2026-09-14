package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/benchmark"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	config := benchmark.DefaultConfig()
	flags := flag.NewFlagSet("bench", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&config.Input, "input", config.Input, "ordered JSONL input file")
	flags.IntVar(&config.MaxEvents, "max-events", config.MaxEvents, "maximum accepted events, 1..100000000 (not a byte limit)")
	flags.IntVar(&config.Samples, "samples", config.Samples, "warm batch samples per engine/case, 1..100")
	flags.DurationVar(&config.Window, "window", config.Window, "minimum duration per batch, >0 and <=1m")
	flags.IntVar(&config.MaxIterations, "max-iterations", config.MaxIterations, "maximum queries per batch, 3..100000000")
	flags.DurationVar(&config.Timeout, "timeout", config.Timeout, "global load/query deadline, >0 and <=24h")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: bench [flags]\n\nCompare row, columnar, and indexed aggregate queries; write one JSON report to stdout.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "bench: no positional arguments accepted")
		return 2
	}
	if err := config.Validate(); err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 2
	}
	report, err := benchmark.Run(ctx, config)
	if err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(stderr, "bench: write report: %v\n", err)
		return 1
	}
	return 0
}
