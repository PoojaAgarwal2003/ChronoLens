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
	"strconv"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/measure"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

type report struct {
	Input       string            `json:"input"`
	InputFormat query.InputFormat `json:"input_format"`
	Filter      query.Filter      `json:"filter"`
	Services    int               `json:"services"`
	LoadMS      *float64          `json:"load_ms"`
	QueryMS     *float64          `json:"query_ms"`
	TimingNote  string            `json:"timing_note,omitempty"`
	Result      query.Result      `json:"result"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("query", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "data/events.jsonl", "ordered JSONL or snapshot input file (selected by -format)")
	formatName := flags.String("format", string(query.JSONLFormat), "input format: jsonl or snapshot")
	engineName := flags.String("engine", "row", "query engine: row, columnar, or indexed")
	maxEvents := flags.Int("max-events", 1000000, "maximum accepted event count (not a byte limit)")
	var filter query.Filter
	flags.Int64Var(&filter.FromUS, "from-us", 0, "inclusive lower Unix timestamp in microseconds")
	flags.Func("to-us", "exclusive upper Unix timestamp in microseconds (default: unbounded)", func(value string) error {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			filter.ToUS = &parsed
		}
		return err
	})
	flags.StringVar(&filter.Service, "service", "", "exact service name (default: all)")
	flags.Func("status", "exact HTTP status, or 0 for all (default: all)", func(value string) error {
		parsed, err := strconv.ParseUint(value, 10, 16)
		if err == nil {
			filter.Status = uint16(parsed)
		}
		return err
	})
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: query [flags]\n\nLoad ordered JSONL or a snapshot and run one aggregate query.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *input == "" || *maxEvents <= 0 {
		fmt.Fprintln(stderr, "query: provide a nonempty input path, positive max-events, and no positional arguments")
		return 2
	}
	engine, err := query.ParseEngine(*engineName)
	if err != nil {
		fmt.Fprintf(stderr, "query: %v\n", err)
		return 2
	}
	if err := filter.Validate(); err != nil {
		fmt.Fprintf(stderr, "query: %v\n", err)
		return 2
	}
	format, err := query.ParseInputFormat(*formatName)
	if err != nil {
		fmt.Fprintf(stderr, "query: %v\n", err)
		return 2
	}
	loadStart := time.Now()
	file, err := os.Open(*input)
	if err != nil {
		fmt.Fprintf(stderr, "query: open input: %v\n", err)
		return 1
	}
	data, loadErr := query.LoadFormat(ctx, file, engine, *maxEvents, format)
	if err := errors.Join(loadErr, file.Close()); err != nil {
		fmt.Fprintf(stderr, "query: load input: %v\n", err)
		return 1
	}
	loadMS := measure.Milliseconds(time.Since(loadStart))
	queryStart := time.Now()
	result, err := data.Query(ctx, filter)
	queryMS := measure.Milliseconds(time.Since(queryStart))
	if err != nil {
		fmt.Fprintf(stderr, "query: execute: %v\n", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	output := report{
		Input: *input, InputFormat: format, Filter: filter, Services: data.ServiceCount(),
		LoadMS: loadMS, QueryMS: queryMS, Result: result,
	}
	if loadMS == nil || queryMS == nil {
		output.TimingNote = "Null timings are below clock resolution; use repeated benchmarks for performance comparisons."
	}
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(stderr, "query: write result: %v\n", err)
		return 1
	}
	return 0
}
