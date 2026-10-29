// SPDX-License-Identifier: Apache-2.0

// Command fake-codex supplies a synthetic offline dependency for conformance only.
package main

import (
	"context"
	"fmt"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/fakecodex"
	"log/slog"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("codex-cli 0.164.0")
		return
	}
	if len(os.Args) != 2 || os.Args[1] != "app-server" {
		os.Exit(2)
	}
	if err := fakecodex.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("Synthetic app-server failed")
		os.Exit(1)
	}
}
