package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Printf("command 1: %v\n", os.Args[1:]) //nolint:forbidigo // Just a test binary
}
