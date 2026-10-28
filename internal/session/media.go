// SPDX-License-Identifier: Apache-2.0

package session

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"slices"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

// Codex echoes user input in both item lifecycle notifications. Keep their
// combined encoded size within the existing 8 MiB routing/ingress budget.
const maxImageBytes = 2 << 20
const maxPromptImages = 16
const maxPromptImageBytes = 2 << 20
const maxImagePixels = 40_000_000

func imageInput(block acp.ContentBlockImage) (schema.UserInput, int, error) {
	bad := setupError("Image must contain a valid bounded PNG, JPEG or GIF")
	format := ""
	switch block.MimeType {
	case "image/png":
		format = "png"
	case "image/jpeg":
		format = "jpeg"
	case "image/gif":
		format = "gif"
	default:
		return schema.UserInput{}, 0, bad
	}
	if block.Data == "" || len(block.Data) > base64.StdEncoding.EncodedLen(maxImageBytes) {
		return schema.UserInput{}, 0, bad
	}
	data, err := base64.StdEncoding.Strict().DecodeString(block.Data)
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return schema.UserInput{}, 0, bad
	}
	config, actual, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || actual != format || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return schema.UserInput{}, 0, bad
	}
	// Ignore the optional URI: the provider never fetches it or opens a path.
	// Canonical base64 avoids forwarding unexpected formatting to the vendor.
	return schema.UserInput{Type: "image", URL: "data:" + block.MimeType + ";base64," + base64.StdEncoding.EncodeToString(data)}, len(data), nil
}

func imageModel(catalog protocol.ModelsResult, model string) bool {
	for _, entry := range catalog.Models {
		if entry.ID == model {
			return slices.Contains(entry.Inputs, "image")
		}
	}
	return false
}
