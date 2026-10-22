package main

import (
	"context"
	"log"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// ensure runs Studio's start-up check of the runtime.
func ensure(ctx context.Context) error {
	up, err := runtime.Ensure(ctx)
	if up != nil {
		log.Printf("     upgraded msb %s to %s", up.From, up.To)
	}
	return err
}
