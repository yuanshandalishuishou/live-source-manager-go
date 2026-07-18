package manager

import (
	"testing"
	"time"
)

func TestParseDailyWait(t *testing.T) {
	d, ok := parseDailyWait("03:00")
	if !ok {
		t.Fatal("valid HH:MM should parse")
	}
	if d <= 0 || d > 24*time.Hour {
		t.Fatalf("duration out of range: %v", d)
	}

	if _, ok := parseDailyWait("99:99"); ok {
		t.Fatal("invalid hour/minute must be rejected")
	}
	if _, ok := parseDailyWait("nope"); ok {
		t.Fatal("non HH:MM must be rejected")
	}
}

func TestScheduleWait(t *testing.T) {
	// interval mode uses intervalHours directly
	if d := scheduleWait("interval", 6, ""); d != 6*time.Hour {
		t.Fatalf("interval wait should be 6h, got %v", d)
	}
	// interval clamped to >=1h
	if d := scheduleWait("interval", 0, ""); d != 24*time.Hour {
		t.Fatalf("interval 0 should clamp to 24h, got %v", d)
	}
	// daily mode delegates to parseDailyWait
	daily, _ := parseDailyWait("03:00")
	if d := scheduleWait("daily", 24, "03:00"); d != daily {
		t.Fatalf("daily wait should equal parseDailyWait result, got %v vs %v", d, daily)
	}
}
