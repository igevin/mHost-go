package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/igevin/mHost-go/internal/hostsfixture"
)

func main() {
	lines := flag.Int("lines", 100000, "number of lines to generate")
	output := flag.String("output", "", "output file")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "usage: hosts-fixture -output <file> [-lines 100000]")
		os.Exit(2)
	}
	file, err := os.Create(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output: %v\n", err)
		os.Exit(1)
	}
	if err := hostsfixture.Generate(file, *lines); err != nil {
		file.Close()
		fmt.Fprintf(os.Stderr, "generate fixture: %v\n", err)
		os.Exit(1)
	}
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close output: %v\n", err)
		os.Exit(1)
	}
}
