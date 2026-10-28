// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"slices"

	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func selection(catalog protocol.ModelsResult, model string, effort protocol.Effort) (string, protocol.Effort, error) {
	if model == "" {
		model = catalog.Default
	}
	for _, entry := range catalog.Models {
		if entry.ID == model {
			if effort != "" && effort != protocol.Effort("auto") && !slices.Contains(entry.Efforts, effort) {
				return "", "", setupError("Selected reasoning effort is unavailable for this model")
			}
			return model, effort, nil
		}
	}
	return "", "", setupError("Selected model is unavailable for this connection")
}

// ConfigOptions returns fresh ACP option trees for the next turn.
func (s *Session) ConfigOptions() []acp.SessionConfigOption {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.options()
}

func (s *Session) options() []acp.SessionConfigOption {
	models := acp.SessionConfigSelectOptionsUngrouped{}
	efforts := acp.SessionConfigSelectOptionsUngrouped{{Name: "Auto", Value: "auto"}}
	for _, m := range s.catalog.Models {
		models = append(models, acp.SessionConfigSelectOption{Name: m.Label, Value: acp.SessionConfigValueId(m.ID)})
		if m.ID == s.snapshot.Model {
			for _, e := range m.Efforts {
				if e != "auto" {
					efforts = append(efforts, acp.SessionConfigSelectOption{Name: string(e), Value: acp.SessionConfigValueId(e)})
				}
			}
		}
	}
	modelCategory, effortCategory := acp.SessionConfigOptionCategoryModel, acp.SessionConfigOptionCategoryThoughtLevel
	effort := s.snapshot.Effort
	if effort == "" {
		effort = "auto"
	}
	return []acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{Id: "model", Name: "Model", Type: "select", Category: &modelCategory, CurrentValue: acp.SessionConfigValueId(s.snapshot.Model), Options: acp.SessionConfigSelectOptions{Ungrouped: &models}}},
		{Select: &acp.SessionConfigOptionSelect{Id: "effort", Name: "Reasoning effort", Type: "select", Category: &effortCategory, CurrentValue: acp.SessionConfigValueId(effort), Options: acp.SessionConfigSelectOptions{Ungrouped: &efforts}}},
	}
}

// SetOption changes only the next-turn selection; a running snapshot is immutable.
func (s *Session) SetOption(ctx context.Context, id, value string) ([]acp.SessionConfigOption, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, setupError("Session is closed")
	}
	model, effort := s.snapshot.Model, s.snapshot.Effort
	switch id {
	case "model":
		model = value
		if _, _, err := selection(s.catalog, model, effort); err != nil {
			effort = ""
		}
	case "effort":
		effort = protocol.Effort(value)
		if value == "auto" {
			effort = ""
		}
	default:
		return nil, setupError("Unknown session option")
	}
	if value == "" {
		return nil, setupError("Session option value is empty")
	}
	if _, _, err := selection(s.catalog, model, effort); err != nil {
		return nil, err
	}
	s.snapshot.Model, s.snapshot.Effort = model, effort
	return s.options(), nil
}
