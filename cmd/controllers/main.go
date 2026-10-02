package main

import (
	"fmt"

	"github.com/dpuig/sextant/pkg/version"
)

func main() {
	fmt.Println("controllers", version.Version)
}
