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
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/load"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("load", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var c load.Config
	flags.StringVar(&c.Target, "target", "http://127.0.0.1:8080/api/query", "numeric loopback query/compare URL; redirects and proxies disabled")
	payload := flags.String("payload", `{"engine":"indexed","from_us":"0","to_us":null,"service":"","status":0,"buckets":100}`, "JSON object, at most 4096 bytes")
	flags.DurationVar(&c.Duration, "duration", 5*time.Second, "arrival window, 1ms..1m")
	flags.IntVar(&c.Rate, "rate", 2, "constant scheduled arrivals/sec, 1..1000 (no retries)")
	flags.IntVar(&c.MaxInflight, "inflight", 2, "local concurrent request cap, 1..64")
	flags.IntVar(&c.MaxRequests, "requests", 1000, "scheduled sample cap, 1..10000")
	flags.DurationVar(&c.Timeout, "timeout", time.Second, "per-request timeout including body, 1ms..10s")
	output := flags.String("output", "-", "new report file, or - for stdout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	c.Payload = json.RawMessage(*payload)
	if flags.NArg() != 0 || *output == "" {
		fmt.Fprintln(stderr, "load: unexpected arguments or empty output")
		return 2
	}
	if err := c.Validate(); err != nil {
		fmt.Fprintln(stderr, "load:", err)
		return 2
	}
	var file *os.File
	if *output != "-" {
		var err error
		file, err = os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			fmt.Fprintln(stderr, "load:", err)
			return 1
		}
		stdout = file
	}
	report, runErr := load.Run(ctx, c)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(report)
	if file != nil {
		writeErr = errors.Join(writeErr, file.Close())
	}
	if err := errors.Join(runErr, writeErr); err != nil {
		fmt.Fprintln(stderr, "load:", err)
		return 1
	}
	// HTTP failures remain data, not hidden by a successful run's exit status.
	return 0
}
