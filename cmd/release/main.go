// release is developer-only automation; it is not included in preview bundles.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/release"
)

func main() {
	var c release.Config
	flag.StringVar(&c.Root, "root", ".", "trusted source repository")
	flag.StringVar(&c.Output, "output", "", "new .zip file in an existing directory; never overwrites")
	flag.StringVar(&c.Version, "version", "", "N.N.N-preview[.N]")
	flag.StringVar(&c.Target, "target", runtime.GOOS+"-"+runtime.GOARCH, "windows-amd64 or linux-amd64")
	flag.BoolVar(&c.AllowDirty, "allow-dirty", false, "explicitly permit fingerprinted dirty local-preview sources")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "release: unexpected positional arguments")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sum, err := release.Build(ctx, c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
	fmt.Printf("%s  %s\n", sum, c.Output)
}
