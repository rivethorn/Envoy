package main

import (
	"log"

	"github.com/rivethorn/envoy/internal/ui"
)

func main() {
	if err := ui.Run(); err != nil {
		log.Fatal(err)
	}
}
