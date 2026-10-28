// SPDX-License-Identifier: Apache-2.0

// Command record-fixtures generates and validates development-only evidence.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/spike"
	"github.com/arvel-dev/arvel-sdk/logx"
)

func main() {
	logger := logx.New("record-fixtures")
	if len(os.Args) > 1 && os.Args[1] == "mcp-fixture" {
		if err := spike.ServeMCP(context.Background(), os.Stdin, os.Stdout); err != nil {
			logger.Error("MCP fixture stopped")
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 || os.Args[1] == "--help" || os.Args[1] == "-h" {
		logger.Info("commands: schema, surface, record, sanitize, verify, sandbox; use COMMAND --help; real probes require ARVEL_E2E=1")
		return
	}
	var o spike.RunOptions
	o.Mode = os.Args[1]
	flags := flag.NewFlagSet(o.Mode, flag.ContinueOnError)
	flags.StringVar(&o.Codex, "codex", "", "absolute Codex binary (default: PATH, or dev-only override)")
	flags.StringVar(&o.Out, "out", "", "new output file or directory")
	flags.StringVar(&o.In, "in", "", "sanitizer input JSONL")
	flags.StringVar(&o.Schema, "schema", "", "exact recorded schema directory")
	flags.StringVar(&o.Index, "index", "", "fixture index (default testdata/fixtures/index.json)")
	flags.StringVar(&o.Notices, "notices", "", "matching upstream LICENSE/NOTICE directory for a new schema")
	flags.StringVar(&o.Home, "home", "private", "private or shared; shared never mutates authentication")
	flags.StringVar(&o.Scenario, "scenario", "handshake", "fixed scenario: "+strings.Join(spike.Scenarios(), ", "))
	flags.DurationVar(&o.Timeout, "timeout", 90*time.Second, "bounded probe deadline")
	if err := flags.Parse(os.Args[2:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(2)
	}
	if flags.NArg() != 0 {
		logger.Error("unexpected positional arguments")
		os.Exit(2)
	}
	if o.Scenario == "device-login" {
		o.DeviceCode = func(url, code string) error {
			// This deliberate interactive challenge is never included in logs or persisted captures.
			_, err := fmt.Fprintf(os.Stderr, "Complete this private Linux probe at %s with code %s\n", url, code)
			return err
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := spike.Run(ctx, o); err != nil {
		logger.Error("probe failed", "error", err)
		os.Exit(1)
	}
	logger.Info("probe completed", "command", o.Mode, "scenario", o.Scenario)
}
