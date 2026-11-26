package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/igevin/mHost-go/internal/legacyprobe"
)

func main() {
	root := flag.String("root", "", "read-only mHost fixture directory")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "usage: storage-probe -root <directory>")
		os.Exit(2)
	}

	report, err := legacyprobe.Scan(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan storage: %v\n", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
		os.Exit(1)
	}
	if len(report.Issues) != 0 {
		for _, issue := range report.Issues {
			fmt.Fprintf(os.Stderr, "%s: %s\n", issue.Path, issue.Message)
		}
		os.Exit(1)
	}
}
