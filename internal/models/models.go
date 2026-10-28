// SPDX-License-Identifier: Apache-2.0

// Package models maps the recorded stable Codex catalog to Arvel model values.
package models

import (
	"errors"
	"slices"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// Map uses the executable model slug as the host ID. Catalog IDs are not aliases.
func Map(catalog []schema.Model, billing protocol.Billing) (protocol.ModelsResult, error) {
	result := protocol.ModelsResult{}
	defaults := 0
	for _, v := range catalog {
		if v.Hidden && !v.IsDefault {
			continue
		}
		model := protocol.Model{ID: v.Model, Label: v.DisplayName, Description: v.Description, Hidden: v.Hidden, Billing: billing, Inputs: slices.Clone(v.InputModalities)}
		if v.InputModalities == nil {
			model.Inputs = []string{"text", "image"}
		}
		if model.ID == "" || model.Label == "" {
			return protocol.ModelsResult{}, errors.New("incomplete Codex model")
		}
		for _, effort := range v.SupportedReasoningEfforts {
			e := protocol.Effort(effort.ReasoningEffort)
			if e.Valid() && !slices.Contains(model.Efforts, e) {
				model.Efforts = append(model.Efforts, e)
			}
		}
		if e := protocol.Effort(v.DefaultReasoningEffort); slices.Contains(model.Efforts, e) {
			model.DefaultEffort = e
		}
		if v.IsDefault {
			defaults++
			result.Default = model.ID
		}
		result.Models = append(result.Models, model)
	}
	if defaults != 1 || result.Validate() != nil {
		return protocol.ModelsResult{}, errors.New("invalid Codex model catalog")
	}
	return result, nil
}

// Cache is a six-hour complete-catalog cache. Its owner serializes all access.
type Cache struct {
	value    protocol.ModelsResult
	at       time.Time
	revision uint64
}

// Get returns a defensive copy only for the current account/process revision.
func (c *Cache) Get(now time.Time, revision uint64, refresh bool) (protocol.ModelsResult, bool) {
	if refresh || c.at.IsZero() || now.Before(c.at) || now.Sub(c.at) >= 6*time.Hour || revision != c.revision {
		return protocol.ModelsResult{}, false
	}
	return clone(c.value), true
}

// Put publishes a complete validated catalog with no shared mutable slices.
func (c *Cache) Put(now time.Time, revision uint64, value protocol.ModelsResult) {
	c.at = now
	c.revision = revision
	c.value = clone(value)
}
func clone(v protocol.ModelsResult) protocol.ModelsResult {
	v.Models = slices.Clone(v.Models)
	for i := range v.Models {
		m := &v.Models[i]
		m.Efforts = slices.Clone(m.Efforts)
		m.Inputs = slices.Clone(m.Inputs)
		m.Aliases = slices.Clone(m.Aliases)
		m.RecommendedFor = slices.Clone(m.RecommendedFor)
	}
	return v
}
