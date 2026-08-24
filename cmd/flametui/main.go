package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/major1201/flametui/pkg/parser"
	"github.com/major1201/flametui/pkg/tui"
)

var version = "1.0.0"

func main() {
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: flametui <profile-file>\n")
		os.Exit(1)
	}

	filename := args[0]

	data, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	prof, err := parser.Parse(data, filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing profile: %v\n", err)
		os.Exit(1)
	}

	app := tui.NewApp(prof)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
