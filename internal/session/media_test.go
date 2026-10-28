// SPDX-License-Identifier: Apache-2.0

package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	acp "github.com/coder/acp-go-sdk"
)

func pixelImage(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

func TestMedia_ImageOrderAndCanonicalInputReachTheVendor(t *testing.T) {
	s, b := sessionFixture(t)
	data := pixelImage(t)
	observed := false
	b.client.hook = func(_ context.Context, method string, params, result any) (bool, error) {
		if method != "turn/start" {
			return false, nil
		}
		input := params.(schema.TurnStartParams).Input
		if len(input) != 3 || input[0].Text != "before" || input[1].Type != "image" || input[1].URL != "data:image/png;base64,"+data || input[2].Text != "after" {
			t.Errorf("unexpected input shape/order")
		}
		observed = true
		return false, nil
	}
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("before"), acp.ImageBlock(data, "image/png"), acp.TextBlock("after")}, &testOutput{}); err != nil || !observed {
		t.Fatalf("image prompt: %v", err)
	}
	if s.Snapshot().Turns != 1 {
		t.Fatal("image prompt lost turn accounting")
	}
}

func TestMedia_InvalidImageDoesNotConsumeOrDeliverATurn(t *testing.T) {
	valid := pixelImage(t)
	for _, tc := range []struct {
		name    string
		content acp.ContentBlock
	}{
		{"empty", acp.ImageBlock("", "image/png")},
		{"base64", acp.ImageBlock("not base64", "image/png")},
		{"format", acp.ImageBlock(valid, "image/jpeg")},
		{"unsupported", acp.ImageBlock(valid, "image/svg+xml")},
		{"bytes", acp.ImageBlock(base64.StdEncoding.EncodeToString([]byte("not an image")), "image/png")},
		{"oversized", acp.ImageBlock(strings.Repeat("A", base64.StdEncoding.EncodedLen(maxImageBytes)+1), "image/png")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, b := sessionFixture(t)
			if _, err := s.Prompt(testContext(t), []acp.ContentBlock{tc.content}, &testOutput{}); err == nil {
				t.Fatal("invalid image accepted")
			}
			if b.client.turns != 0 || s.Snapshot().Turns != 0 || !s.Snapshot().Empty {
				t.Fatal("invalid image consumed a turn")
			}
		})
	}
}

func TestMedia_TextOnlyModelAndExcessiveImageCountAreRejected(t *testing.T) {
	s, b := sessionFixture(t)
	if _, err := s.SetOption(testContext(t), "model", "other"); err != nil {
		t.Fatal(err)
	}
	content := acp.ImageBlock(pixelImage(t), "image/png")
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{content}, &testOutput{}); err == nil || b.client.turns != 0 || !s.Snapshot().Empty {
		t.Fatal("text-only model accepted image or consumed history")
	}
	if _, err := promptInput(makeImages(content, maxPromptImages+1)); err == nil {
		t.Fatal("excessive image count accepted")
	}
}

func makeImages(content acp.ContentBlock, n int) []acp.ContentBlock {
	result := make([]acp.ContentBlock, n)
	for i := range result {
		result[i] = content
	}
	return result
}
