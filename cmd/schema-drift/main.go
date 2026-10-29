// SPDX-License-Identifier: Apache-2.0

// Command schema-drift checks local generated schemas without accessing the network.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/schemadrift"
)

func run(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("schema-drift", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	baseline := flags.String("baseline", "testdata/schema/0.164.0", "recorded baseline directory")
	candidate := flags.String("candidate", "", "generated candidate directory")
	surface := flags.String("surface", "testdata/schema/0.164.0/production-surface.json", "production method inventory")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *candidate == "" {
		fmt.Fprintln(diagnostics, "schema-drift: baseline, candidate and surface must be valid local inputs")
		return 2
	}
	findings, err := schemadrift.Compare(ctx, *baseline, *candidate, *surface)
	if err != nil {
		fmt.Fprintln(diagnostics, "schema-drift: invalid schema input or unresolved local reference")
		return 2
	}
	if findings == nil {
		findings = []schemadrift.Finding{}
	}
	if json.NewEncoder(out).Encode(findings) != nil {
		return 2
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
