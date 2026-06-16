package vitalscmd

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildBody_dailyIsDateOnly(t *testing.T) {
	now := time.Date(2026, 6, 16, 13, 30, 0, 0, time.UTC)
	body, err := buildBody([]string{"crashRate"}, []string{"versionCode"}, "DAILY", "", 28*24*time.Hour, now)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	var parsed struct {
		Metrics      []string `json:"metrics"`
		Dimensions   []string `json:"dimensions"`
		Filter       string   `json:"filter"`
		TimelineSpec struct {
			AggregationPeriod string         `json:"aggregationPeriod"`
			StartTime         map[string]int `json:"startTime"`
			EndTime           map[string]int `json:"endTime"`
		} `json:"timelineSpec"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.TimelineSpec.AggregationPeriod != "DAILY" {
		t.Errorf("period = %q", parsed.TimelineSpec.AggregationPeriod)
	}
	if parsed.TimelineSpec.EndTime["day"] != 16 || parsed.TimelineSpec.EndTime["month"] != 6 {
		t.Errorf("endTime = %v, want 2026-06-16", parsed.TimelineSpec.EndTime)
	}
	if _, hasHours := parsed.TimelineSpec.EndTime["hours"]; hasHours {
		t.Errorf("DAILY endTime must be date-only, got hours: %v", parsed.TimelineSpec.EndTime)
	}
	if parsed.TimelineSpec.StartTime["day"] != 19 || parsed.TimelineSpec.StartTime["month"] != 5 {
		t.Errorf("startTime = %v, want 2026-05-19 (28d before)", parsed.TimelineSpec.StartTime)
	}
	if len(parsed.Dimensions) != 1 || parsed.Dimensions[0] != "versionCode" {
		t.Errorf("dimensions = %v", parsed.Dimensions)
	}
	if parsed.Filter != "" {
		t.Errorf("filter = %q, want empty when none given", parsed.Filter)
	}
}

func TestBuildBody_hourlyCarriesHour_andFilter(t *testing.T) {
	now := time.Date(2026, 6, 16, 13, 30, 0, 0, time.UTC)
	body, err := buildBody([]string{"anrRate"}, nil, "HOURLY", "versionCode = 123", 24*time.Hour, now)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	var parsed struct {
		Filter       string `json:"filter"`
		TimelineSpec struct {
			EndTime map[string]int `json:"endTime"`
		} `json:"timelineSpec"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.TimelineSpec.EndTime["hours"] != 13 {
		t.Errorf("HOURLY endTime hours = %v, want 13", parsed.TimelineSpec.EndTime["hours"])
	}
	if parsed.Filter != "versionCode = 123" {
		t.Errorf("filter = %q, want versionCode = 123", parsed.Filter)
	}
}

// TestBuildBody_hourlyMidnightKeepsZeroHour pins the *int fix: an HOURLY window
// whose boundary lands on midnight must carry an explicit "hours":0, not omit it
// (which would send a date-only datetime for an HOURLY request).
func TestBuildBody_hourlyMidnightKeepsZeroHour(t *testing.T) {
	now := time.Date(2026, 6, 16, 0, 30, 0, 0, time.UTC) // 00:30 → end truncates to 00:00
	body, err := buildBody([]string{"crashRate"}, nil, "HOURLY", "", time.Hour, now)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	var parsed struct {
		TimelineSpec struct {
			EndTime map[string]json.RawMessage `json:"endTime"`
		} `json:"timelineSpec"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	h, ok := parsed.TimelineSpec.EndTime["hours"]
	if !ok {
		t.Fatalf("HOURLY midnight endTime must include the hours field: %v", parsed.TimelineSpec.EndTime)
	}
	if string(h) != "0" {
		t.Errorf("endTime hours = %s, want 0", h)
	}
}

func TestParseSince(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 28 * 24 * time.Hour, false},
		{"28d", 28 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"0d", 0, true},
		{"-5d", 0, true},
		{"banana", 0, true},
	}
	for _, c := range cases {
		got, err := ParseSince(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseSince(%q): want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSince(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSince(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
