package main

import (
	"fmt"
	"nanocc/agents/runtime"
	"os"
)

func main() {
	if err := runtime.RunInteractive(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
