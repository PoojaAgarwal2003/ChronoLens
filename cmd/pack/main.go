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
	"syscall"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/measure"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "data/events.jsonl", "ordered JSONL input file")
	output := flags.String("output", "data/events.clens", "new snapshot file; never replaces an existing path")
	maxEvents := flags.Int("max-events", 1000000, "accepted event limit, 1-100000000 (not a byte limit)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: pack [flags]\n\nConvert validated JSONL to an immutable, checksummed snapshot.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *input == "" || *input == "-" || *output == "" || *output == "-" ||
		*maxEvents < 1 || *maxEvents > 100000000 {
		fmt.Fprintln(stderr, "pack: use nonempty file paths, max-events between 1 and 100000000, and no positional arguments")
		return 2
	}
	start := time.Now()
	result, err := pack(ctx, *input, *output, *maxEvents, os.Link)
	if err != nil {
		fmt.Fprintf(stderr, "pack: %v\n", err)
		return 1
	}
	result.PackMS = measure.Milliseconds(time.Since(start))
	if result.PackMS == nil {
		result.TimingNote = "Pack timing is below clock resolution, not zero."
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(stderr, "pack: write report (snapshot is already published): %v\n", err)
		return 1
	}
	return 0
}
