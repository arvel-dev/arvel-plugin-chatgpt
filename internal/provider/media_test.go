// SPDX-License-Identifier: Apache-2.0

package provider_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"

	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func TestProvider_SDKAdvertisesAndDeliversInlineImage(t *testing.T) {
	h := newSDKHost(t, nil)
	if !h.images {
		t.Fatal("image prompt capability is absent on SDK wire")
	}
	id := h.newSession(t, protocol.SessionMeta{})
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	var response acp.PromptResponse
	err := h.peer.Call(h.ctx, protocol.MethodSessionPrompt, map[string]any{"sessionId": id, "prompt": []acp.ContentBlock{acp.TextBlock("expect-image"), acp.ImageBlock(base64.StdEncoding.EncodeToString(data.Bytes()), "image/png")}}, &response)
	if err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("SDK image turn: %+v %v", response, err)
	}
}
