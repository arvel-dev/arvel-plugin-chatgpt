// SPDX-License-Identifier: Apache-2.0

// Package sanitize removes session-owned literal secrets from vendor output.
package sanitize

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/arvel-dev/arvel-sdk/redact"
)

// Cleaner is immutable and safe for concurrent session callbacks.
type Cleaner struct {
	values  []string
	replace *strings.Replacer
	longest int
}

// New copies literal values, including short credentials ignored by heuristics.
func New(values []string) *Cleaner {
	c := &Cleaner{}
	values = slices.Clone(values)
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	var pairs []string
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		c.values = append(c.values, value)
		c.longest = max(c.longest, len(value))
		pairs = append(pairs, value, redact.Replacement)
	}
	c.replace = strings.NewReplacer(pairs...)
	return c
}

// String also applies the SDK's standard token patterns.
func (c *Cleaner) String(text string) string {
	if c != nil {
		text = c.replace.Replace(text)
	}
	return (*redact.Redactor)(nil).String(text)
}

// Split emits a safe prefix and retains a bounded raw suffix between deltas.
// Callers own one suffix per text stream and flush it on completion.
func (c *Cleaner) Split(text string, final bool) (string, string) {
	if final || c == nil || c.longest == 0 {
		return c.String(text), ""
	}
	cut := max(0, len(text)-c.longest+1)
	for cut > 0 && cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut--
	}
	// A complete literal may span the proposed cut. Keep it whole until emission.
	for {
		previous := cut
		for _, value := range c.values {
			start := max(0, cut-len(value)+1)
			if i := strings.Index(text[start:], value); i >= 0 {
				i += start
				if i < cut && i+len(value) > cut {
					cut = i
				}
			}
		}
		if previous == cut {
			break
		}
	}
	return c.String(text[:cut]), text[cut:]
}

// Combine builds a new immutable cleaner without exposing its credential values.
func Combine(cleaners ...*Cleaner) *Cleaner {
	var values []string
	for _, cleaner := range cleaners {
		if cleaner != nil {
			values = append(values, cleaner.values...)
		}
	}
	return New(values)
}
