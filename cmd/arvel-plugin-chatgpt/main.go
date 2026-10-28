// SPDX-License-Identifier: Apache-2.0

// Command arvel-plugin-chatgpt serves the ChatGPT provider over ACP stdio.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/provider"
	"github.com/arvel-dev/arvel-sdk/plugin"
)

var version = "0.1.0"

func main() {
	if err := plugin.Run(context.Background(), provider.New(), plugin.WithVersion("arvel-plugin-chatgpt", version)); err != nil {
		// External errors may contain credentials; report only a fixed safe message.
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("Provider stopped with an error")
		os.Exit(1)
	}
}
