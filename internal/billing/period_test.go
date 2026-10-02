package billing

import (
	"errors"
	"testing"
	"time"
)

func TestPeriodBounds(t *testing.T) {
	tests := []struct{ name, now, period, zone, start, end string }{
		{"UTC default", "2026-09-30T12:34:56Z", "daily", "", "2026-09-30T00:00:00Z", "2026-10-01T00:00:00Z"},
		{"Sunday belongs to Monday", "2026-10-04T12:00:00Z", "weekly", "UTC", "2026-09-28T00:00:00Z", "2026-10-05T00:00:00Z"},
		{"Monday boundary", "2026-10-05T00:00:00Z", "weekly", "UTC", "2026-10-05T00:00:00Z", "2026-10-12T00:00:00Z"},
		{"leap February", "2024-02-29T20:00:00Z", "monthly", "UTC", "2024-02-01T00:00:00Z", "2024-03-01T00:00:00Z"},
		{"year rollover", "2026-12-31T12:00:00Z", "monthly", "UTC", "2026-12-01T00:00:00Z", "2027-01-01T00:00:00Z"},
		{"Shanghai next local day", "2026-09-30T18:00:00Z", "daily", "Asia/Shanghai", "2026-09-30T16:00:00Z", "2026-10-01T16:00:00Z"},
		{"spring short day", "2026-03-08T12:00:00Z", "daily", "America/New_York", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z"},
		{"fall long day", "2026-11-01T12:00:00Z", "daily", "America/New_York", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		{"spring short week", "2026-03-08T12:00:00Z", "weekly", "America/New_York", "2026-03-02T05:00:00Z", "2026-03-09T04:00:00Z"},
		{"midnight gap day", "2026-09-06T12:00:00Z", "daily", "America/Santiago", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z"},
		{"last hour of gap day", "2026-09-07T02:30:00Z", "daily", "America/Santiago", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z"},
		{"day before midnight gap", "2026-09-05T12:00:00Z", "daily", "America/Santiago", "2026-09-05T04:00:00Z", "2026-09-06T04:00:00Z"},
		{"week before midnight gap", "2026-09-05T12:00:00Z", "weekly", "America/Santiago", "2026-08-31T04:00:00Z", "2026-09-07T03:00:00Z"},
		{"same week on midnight gap day", "2026-09-06T12:00:00Z", "weekly", "America/Santiago", "2026-08-31T04:00:00Z", "2026-09-07T03:00:00Z"},
		{"first repeated midnight", "2026-11-01T04:30:00Z", "daily", "America/Havana", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		{"second repeated midnight", "2026-11-01T05:30:00Z", "daily", "America/Havana", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		{"month starts with midnight gap", "2017-10-15T12:00:00Z", "monthly", "America/Asuncion", "2017-10-01T04:00:00Z", "2017-11-01T03:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			start, end, err := PeriodBounds(now, tt.period, tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			if start.Format(time.RFC3339) != tt.start || end.Format(time.RFC3339) != tt.end {
				t.Fatalf("[%s,%s), want [%s,%s)", start, end, tt.start, tt.end)
			}
			if now.Before(start) || !now.Before(end) {
				t.Fatal("instant outside own period")
			}
		})
	}
	for _, tt := range []struct{ period, zone string }{{"yearly", "UTC"}, {"daily", "Invalid/Zone"}, {"daily", "Local"}} {
		if _, _, err := PeriodBounds(time.Now(), tt.period, tt.zone); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("invalid %+v = %v", tt, err)
		}
	}
}

func TestGroupScopeRoundTripAndNoAliases(t *testing.T) {
	seen := map[string]bool{}
	for _, pair := range [][2]string{{"a", "b"}, {"a/b", "c"}, {"a", "b/c"}, {"a%2Fb", "c"}, {"space name", "a+b"}, {"中文", "研发"}} {
		scope := GroupScope(pair[0], pair[1])
		if seen[scope] {
			t.Fatalf("scope collision %s", scope)
		}
		seen[scope] = true
		tenant, group, err := ParseGroupScope(scope)
		if err != nil || tenant != pair[0] || group != pair[1] {
			t.Fatalf("roundtrip %q = %q %q %v", scope, tenant, group, err)
		}
	}
	if GroupScope("acme", "team") != "group:acme/team" {
		t.Fatal("plain scope compatibility")
	}
	for _, scope := range []string{"group:team", "group:a/b/c", "group:/b", "group:a/", "group:a/%ZZ", "group:a/%62", "a/b"} {
		if _, _, err := ParseGroupScope(scope); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("ambiguous scope %q accepted: %v", scope, err)
		}
	}
}
