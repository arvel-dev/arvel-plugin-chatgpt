// SPDX-License-Identifier: Apache-2.0
//go:build windows

package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/procx"
	"golang.org/x/sys/windows"
)

func windowsFixture(t *testing.T) (string, string) {
	t.Helper()
	// Temp cleanup is registered before process cleanup (testing uses LIFO).
	root := filepath.Join(t.TempDir(), "Arvel & tools")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(current)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	exe := filepath.Join(root, "owned vendor.exe")
	output, err := os.Create(exe)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy fixture: %v %v", copyErr, closeErr)
	}
	shim := filepath.Join(root, "codex.cmd")
	// Fixed owned fixture, not an alternative production shell launcher.
	if err := os.WriteFile(shim, []byte("@echo off\r\n@\"%~dp0owned vendor.exe\" %*\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return exe, shim
}

func TestWindows_StandaloneAndNPMShimInitializeAndTerminateTree(t *testing.T) {
	exe, shim := windowsFixture(t)
	for _, path := range []string{exe, shim} {
		t.Run(filepath.Ext(path), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			version, _, err := procx.Version(ctx, path)
			if err != nil || version != "0.164.0" {
				t.Fatalf("version: %s %v", version, err)
			}
			options := fakeOptions(t, "windows-tree")
			options.Path = path
			client, err := Start(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close(context.Background()) })
			var result struct {
				ChildPID uint32 `json:"childPid"`
			}
			if err := client.Call(ctx, "account/read", nil, &result); err != nil || result.ChildPID == 0 {
				t.Fatalf("child: %+v %v", result, err)
			}
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, result.ChildPID)
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(handle)
			if err := client.Close(ctx); err != nil {
				t.Fatal(err)
			}
			state, err := windows.WaitForSingleObject(handle, 5000)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatalf("child still alive: %d %v", state, err)
			}
			select {
			case <-client.Done():
			default:
				t.Fatal("client workers not joined")
			}
		})
	}
}

func TestWindows_SDKShimArgumentsAreQuotedOrRejected(t *testing.T) {
	_, shim := windowsFixture(t)
	args := []string{"space value", "a&b", "x^y", "(value)", `trailing\`}
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	process, err := procx.Start(ctx, procx.Spec{Path: shim, Args: append([]string{"arvel-owned-argv"}, args...), Stdout: &output})
	if err != nil {
		t.Fatal(err)
	}
	code, err := process.Wait()
	if err != nil || code != 0 {
		t.Fatalf("fixture: %d %v", code, err)
	}
	var received []string
	if json.Unmarshal(output.Bytes(), &received) != nil || !reflect.DeepEqual(received, args) {
		t.Fatalf("arguments changed: %q", received)
	}
	for _, arg := range []string{`bad"quote`, "%EXPAND%", "!expand!", "line\nbreak"} {
		process, err := procx.Start(ctx, procx.Spec{Path: shim, Args: []string{"arvel-owned-argv", arg}})
		if err == nil {
			process.KillTree(0)
			_, _ = process.Wait()
			t.Fatal("unsafe shim argument accepted")
		}
		if !errors.Is(err, procx.ErrUnsafeArg) {
			t.Fatal("unexpected shim rejection")
		}
	}
}
