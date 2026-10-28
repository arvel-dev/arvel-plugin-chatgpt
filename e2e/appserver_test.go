//go:build e2e

// SPDX-License-Identifier: Apache-2.0
package e2e_test

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/procx"
)

func TestAppServer_TransportHandshake(t *testing.T) {
	if os.Getenv("ARVEL_E2E") != "1" {
		t.Fatal("live probes require ARVEL_E2E=1")
	}
	binary, err := procx.FindBinary("codex")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := appserver.Start(ctx, appserver.Options{Path: binary, Dir: t.TempDir(), Env: procx.Env().Set("CODEX_HOME", t.TempDir()).Build(), LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var account schema.AccountReadResponse
	if err := c.Call(ctx, "account/read", schema.AccountReadParams{}, &account); err != nil {
		t.Fatal(err)
	}
	if account.Account != nil {
		t.Fatal("empty private home inherited an account")
	}
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
