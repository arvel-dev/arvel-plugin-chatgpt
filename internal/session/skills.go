// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/skillset"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func (s *Session) prepareSkills(ctx context.Context, cwd string) error {
	s.mu.Lock()
	manager, release := s.skillManager, s.skillRelease
	refs := s.params.Meta.Skills
	s.mu.Unlock()
	if manager == nil || release != nil {
		return nil
	}
	catalog, cleanup, err := manager.Acquire(ctx, cwd, refs)
	if err != nil {
		if errors.Is(err, skillset.ErrBusy) {
			return protocol.NewError(protocol.ErrBusy, "Cannot replace the skill selection in this shared workspace; use a separate task directory", nil)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return setupError("Cannot prepare assigned workspace skills")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || ctx.Err() != nil {
		cleanup()
		return setupError("Workspace skill setup was cancelled")
	}
	s.skillCatalog, s.skillRelease = catalog, cleanup
	return nil
}

func (s *Session) verifySkills(ctx context.Context, client caller, cwd string) error {
	var response schema.SkillsListResponse
	if err := client.Call(ctx, "skills/list", schema.SkillsListParams{CWDs: []string{cwd}, ForceReload: true}, &response); err != nil {
		return setupError("Cannot verify the native skill catalog")
	}
	s.mu.Lock()
	catalog := s.skillCatalog
	s.mu.Unlock()
	for _, owned := range catalog {
		found := false
		for _, entry := range response.Data {
			if filepath.Clean(entry.CWD) != filepath.Clean(cwd) {
				continue
			}
			for _, skill := range entry.Skills {
				if filepath.Clean(skill.Path) == filepath.Join(owned.Path, "SKILL.md") && skill.Name == owned.Name && skill.Enabled {
					found = true
				}
			}
		}
		if !found {
			return setupError("An assigned skill is unavailable in the native catalog")
		}
	}
	return nil
}

func selectedSkills(input []schema.UserInput, source, owned []protocol.SkillRef) []schema.UserInput {
	for i, ref := range source {
		if i >= len(owned) {
			break
		}
		mentioned := false
		for _, block := range input {
			if block.Type == "text" && skillMention(block.Text, ref.Name) {
				mentioned = true
				break
			}
		}
		if mentioned {
			input = append(input, schema.UserInput{Type: "skill", Name: owned[i].Name, Path: filepath.Join(owned[i].Path, "SKILL.md")})
		}
	}
	return input
}

func skillMention(text, name string) bool {
	token := "$" + name
	for offset := 0; offset < len(text); {
		found := strings.Index(text[offset:], token)
		if found < 0 {
			return false
		}
		index := offset + found
		end := index + len(token)
		escaped := index > 0 && text[index-1] == '\\'
		if !escaped {
			if end == len(text) {
				return true
			}
			next, _ := utf8.DecodeRuneInString(text[end:])
			if !unicode.IsLetter(next) && !unicode.IsDigit(next) && !strings.ContainsRune("._-", next) {
				return true
			}
		}
		offset = end
	}
	return false
}
