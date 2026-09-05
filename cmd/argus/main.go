package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 || os.Args[1] != "version" {
		fmt.Fprintln(os.Stderr, "usage: argus version")
		os.Exit(1)
	}

	fmt.Println(version)
}
