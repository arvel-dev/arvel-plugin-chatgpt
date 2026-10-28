// SPDX-License-Identifier: Apache-2.0

// Package usage maps stable vendor measurements and errors to host contracts.
package usage

import (
	"fmt"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// Freshness is the maximum age for a quota reset hint and active refresh interval.
const Freshness = 10 * time.Minute

// QuotaContext is a connection-scoped observation; its owner validates identity.
type QuotaContext struct {
	Usage      protocol.Usage
	ObservedAt time.Time
}

// Unknown never infers spend or unlimited capacity from missing observations.
func Unknown(kind string) protocol.Usage {
	u := protocol.Usage{Source: protocol.UsageSourceUnknown, Status: protocol.UsageStatusOK}
	if kind == "api_key" {
		u.Spend = &protocol.Spend{Currency: "USD", Source: protocol.UsageSourceUnknown}
	}
	return u
}

// Snapshot maps only the historical quota bucket, preserving window slot IDs.
func Snapshot(snapshot schema.RateLimitSnapshot, kind string, now time.Time) protocol.Usage {
	u := Unknown(kind)
	if kind != "chatgpt" {
		return u
	}
	for _, entry := range []struct {
		id     string
		window *schema.RateLimitWindow
	}{{"primary", snapshot.Primary}, {"secondary", snapshot.Secondary}} {
		w := entry.window
		if w == nil || w.UsedPercent == nil || *w.UsedPercent < 0 || *w.UsedPercent > 100 {
			continue
		}
		v := protocol.UsageWindow{ID: entry.id, UsedPercent: float64(*w.UsedPercent)}
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			reset := time.Unix(*w.ResetsAt, 0).UTC()
			if reset.Year() >= 0 && reset.Year() <= 9999 {
				v.ResetsAt = reset
			}
		}
		if w.WindowDurationMins != nil && *w.WindowDurationMins > 0 && int64(int(*w.WindowDurationMins)) == *w.WindowDurationMins {
			v.DurationMinutes = int(*w.WindowDurationMins)
			switch v.DurationMinutes {
			case 300:
				v.Label = "5-hour window"
			case 10080:
				v.Label = "Week"
			default:
				v.Label = fmt.Sprintf("%d-minute window", v.DurationMinutes)
			}
		}
		u.Windows = append(u.Windows, v)
		if v.UsedPercent >= 80 {
			u.Status = protocol.UsageStatusWarning
		}
	}
	if len(u.Windows) > 0 {
		u.Source = protocol.UsageSourceExact
		u.UpdatedAt = now
	}
	if snapshot.RateLimitReachedType != nil && *snapshot.RateLimitReachedType != "" {
		u.Status = protocol.UsageStatusExhausted
	}
	return u
}
