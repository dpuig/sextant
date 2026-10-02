package main

import (
	"fmt"

	"github.com/dpuig/sextant/pkg/version"
)

func main() {
	fmt.Println("agent", version.Version)
}
