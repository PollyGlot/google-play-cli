package main

import (
	"fmt"
	"os"
)

// Build-time variables injected by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Printf("gplay %s (%s, %s)\n", version, commit, date)
			return
		}
	}

	fmt.Println("gplay — Google Play Developer CLI")
	fmt.Println()
	fmt.Println("This is a placeholder binary. Real commands are under construction.")
	fmt.Println("See https://github.com/PollyGlot/google-play-cli for status and design docs.")
	os.Exit(0)
}
