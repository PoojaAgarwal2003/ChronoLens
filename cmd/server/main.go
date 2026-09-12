package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/api"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "data/events.jsonl", "ordered JSONL dataset")
	web := flags.String("web", "web/dist", "built frontend directory")
	listen := flags.String("listen", "127.0.0.1:8080", "numeric loopback address and port")
	maxEvents := flags.Int("max-events", 1000000, "accepted event limit (1-10000000); all layouts remain in memory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	host, port, err := net.SplitHostPort(*listen)
	ip := net.ParseIP(host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || portNumber < 0 || portNumber > 65535 || flags.NArg() != 0 ||
		*input == "" || *web == "" || *maxEvents < 1 || *maxEvents > 10000000 {
		fmt.Fprintln(stderr, "server: use a numeric loopback host:port, nonempty input/web paths, and max-events between 1 and 10000000")
		return 2
	}
	start := time.Now()
	file, err := os.Open(*input)
	if err != nil {
		fmt.Fprintf(stderr, "server: open dataset: %v\n", err)
		return 1
	}
	catalog, loadErr := query.LoadCatalog(ctx, file, *maxEvents)
	if err := errors.Join(loadErr, file.Close()); err != nil {
		fmt.Fprintf(stderr, "server: load dataset: %v\n", err)
		return 1
	}
	handler, err := api.New(catalog, os.DirFS(*web), filepath.Base(*input), time.Since(start))
	if err != nil {
		fmt.Fprintf(stderr, "server: %v\n", err)
		return 1
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(stderr, "server: listen: %v\n", err)
		return 1
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	fmt.Fprintf(stdout, "ChronoLens listening on http://%s\n", listener.Addr())
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "server: serve: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			fmt.Fprintf(stderr, "server: shutdown: %v\n", errors.Join(err, server.Close()))
			return 1
		}
	}
	return 0
}
