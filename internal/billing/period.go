package billing

import (
	"fmt"
	"time"
)

// PeriodBounds computes calendar dates before resolving their local day starts.
// A skipped or repeated midnight must not change the period's calendar dates.
func PeriodBounds(now time.Time, period, timezone string) (time.Time, time.Time, error) {
	if timezone == "" {
		timezone = "UTC"
	}
	if timezone == "Local" {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: timezone must be explicit", ErrInvalidPolicy)
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: timezone %q", ErrInvalidPolicy, timezone)
	}
	local := now.In(loc)
	// UTC here is a calendar coordinate, not the policy's actual boundary.
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	var end time.Time
	switch period {
	case "daily":
		end = start.AddDate(0, 0, 1)
	case "weekly":
		start = start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		end = start.AddDate(0, 0, 7)
	case "monthly":
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 1, 0)
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("%w: period %q", ErrInvalidPolicy, period)
	}
	return localDayStart(start, loc), localDayStart(end, loc), nil
}

// localDayStart resolves midnight against the adjacent constant-offset intervals.
// In a gap the first valid instant is the transition; in a fold the earlier
// midnight wins. ZoneBounds avoids assuming the transition's size or wall time.
func localDayStart(date time.Time, loc *time.Location) time.Time {
	probe := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	start, end := probe.ZoneBounds()
	probes := []time.Time{probe}
	if !start.IsZero() {
		probes = append(probes, start.Add(-time.Nanosecond))
	}
	if !end.IsZero() {
		probes = append(probes, end)
	}
	var earliest time.Time
	found := false
	for _, p := range probes {
		lower, upper := p.ZoneBounds()
		_, offset := p.Zone()
		candidate := date.Add(-time.Duration(offset) * time.Second)
		if !lower.IsZero() && candidate.Before(lower) {
			candidate = lower
		}
		if !upper.IsZero() && !candidate.Before(upper) {
			continue
		}
		if !found || candidate.Before(earliest) {
			earliest, found = candidate, true
		}
	}
	return earliest.UTC()
}
