// SPDX-License-Identifier: Apache-2.0
package skillset

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/safefile"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"gopkg.in/yaml.v3"
)

// nativePolicy changes only a freshly SDK-materialized owned copy.
func nativePolicy(ref protocol.SkillRef) error {
	if err := nativeName(ref); err != nil {
		return err
	}
	relative := filepath.Join("agents", "openai.yaml")
	metadata := map[string]any{}
	file, err := safefile.Open(ref.Path, relative)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		_ = file.Close()
		if readErr != nil || len(data) > 1<<20 || yaml.Unmarshal(data, &metadata) != nil {
			return ErrSource
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrSource
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	policy := map[string]any{}
	if value, ok := metadata["policy"]; ok && value != nil {
		var valid bool
		policy, valid = value.(map[string]any)
		if !valid {
			return ErrSource
		}
	}
	policy["allow_implicit_invocation"] = ref.Invocation != protocol.SkillInvocationManual
	metadata["policy"] = policy
	encoded, err := yaml.Marshal(metadata)
	if err != nil {
		return ErrSource
	}
	dir := filepath.Join(ref.Path, "agents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ErrSource
	}
	if err := os.WriteFile(filepath.Join(ref.Path, relative), encoded, 0600); err != nil {
		return ErrSource
	}
	return nil
}

func nativeName(ref protocol.SkillRef) error {
	file, err := safefile.Open(ref.Path, "SKILL.md")
	if err != nil {
		return ErrSource
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	_ = file.Close()
	if err != nil || len(data) > 1<<20 {
		return ErrSource
	}
	lines := bytes.SplitAfter(data, []byte{'\n'})
	trim := func(line []byte) string { return string(bytes.TrimRight(line, "\r\n")) }
	if len(lines) < 3 || trim(lines[0]) != "---" {
		return ErrSource
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if trim(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return ErrSource
	}
	var metadata map[string]any
	if yaml.Unmarshal(bytes.Join(lines[1:end], nil), &metadata) != nil || metadata == nil {
		return ErrSource
	}
	metadata["name"] = ref.Name
	header, err := yaml.Marshal(metadata)
	if err != nil {
		return ErrSource
	}
	eol := []byte{'\n'}
	if bytes.HasSuffix(lines[0], []byte("\r\n")) {
		eol = []byte("\r\n")
		header = bytes.ReplaceAll(header, []byte{'\n'}, eol)
	}
	var result bytes.Buffer
	result.WriteString("---")
	result.Write(eol)
	result.Write(header)
	result.WriteString("---")
	result.Write(eol)
	result.Write(bytes.Join(lines[end+1:], nil))
	if os.WriteFile(filepath.Join(ref.Path, "SKILL.md"), result.Bytes(), 0600) != nil {
		return ErrSource
	}
	return nil
}
