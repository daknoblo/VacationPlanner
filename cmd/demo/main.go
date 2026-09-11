// Command demo generates a fully offline, read-only application showcase.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/daknoblo/vacationplanner/internal/demo"
)

func main() {
	out := flag.String("out", "dist", "output directory (unrelated existing files are never overwritten)")
	version := flag.String("version", "development", "version label displayed in the demo and documentation")
	readme := flag.String("readme", "README.md", "local README to include as escaped documentation; empty to omit")
	flag.Parse()
	if err := demo.Build(context.Background(), demo.Options{Out: *out, Version: *version, Readme: *readme}); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
	fmt.Printf("Static documentation and English/German demos generated in %s\n", *out)
}
