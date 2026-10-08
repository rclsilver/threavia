package schedule

import (
	"testing"
	"time"
)

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("time zone %s unavailable: %v", name, err)
	}
	return location
}

// TestNextFollowsTheExpression pins the grammar people already know, read in
// the zone it was written in.
func TestNextFollowsTheExpression(t *testing.T) {
	t.Parallel()
	paris := mustLocation(t, "Europe/Paris")
	// A Thursday, 08:30 in Paris.
	from := time.Date(2026, 10, 8, 8, 30, 0, 0, paris)

	cases := []struct {
		expression string
		want       time.Time
	}{
		{"0 8 * * *", time.Date(2026, 10, 9, 8, 0, 0, 0, paris)},
		{"45 8 * * *", time.Date(2026, 10, 8, 8, 45, 0, 0, paris)},
		{"*/15 * * * *", time.Date(2026, 10, 8, 8, 45, 0, 0, paris)},
		{"0 9-17/4 * * *", time.Date(2026, 10, 8, 9, 0, 0, 0, paris)},
		{"0 8 * * mon-fri", time.Date(2026, 10, 9, 8, 0, 0, 0, paris)},
		{"0 8 * * 1", time.Date(2026, 10, 12, 8, 0, 0, 0, paris)},
		{"0 8 * * 7", time.Date(2026, 10, 11, 8, 0, 0, 0, paris)},
		{"0 0 1 * *", time.Date(2026, 11, 1, 0, 0, 0, 0, paris)},
		{"0 0 1 jan *", time.Date(2027, 1, 1, 0, 0, 0, 0, paris)},
		{"30 8 8 * *", time.Date(2026, 11, 8, 8, 30, 0, 0, paris)},
		// Both day fields restricted: either one is enough.
		{"0 12 15 * fri", time.Date(2026, 10, 9, 12, 0, 0, 0, paris)},
		{"0 0 29 2 *", time.Date(2028, 2, 29, 0, 0, 0, 0, paris)},
	}
	for _, c := range cases {
		spec, err := Parse(c.expression)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.expression, err)
		}
		got, err := spec.Next(from, paris)
		if err != nil {
			t.Fatalf("Next(%q): %v", c.expression, err)
		}
		if !got.Equal(c.want) {
			t.Errorf("Next(%q) = %s, want %s", c.expression, got, c.want)
		}
	}
}

// TestADaylightSavingChangeIsAClockChange pins what a person who wrote "every
// day at 2:30" means: a skipped 2:30 does not fire that day, and a repeated
// one fires once.
func TestADaylightSavingChangeIsAClockChange(t *testing.T) {
	t.Parallel()
	paris := mustLocation(t, "Europe/Paris")
	spec, err := Parse("30 2 * * *")
	if err != nil {
		t.Fatal(err)
	}

	// 2026-03-29: 02:00 jumps to 03:00 in Paris.
	got, err := spec.Next(time.Date(2026, 3, 28, 12, 0, 0, 0, paris), paris)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 30, 2, 30, 0, 0, paris); !got.Equal(want) {
		t.Errorf("across the spring change: %s, want %s", got, want)
	}

	// 2026-10-25: 03:00 falls back to 02:00, so 02:30 happens twice.
	first, err := spec.Next(time.Date(2026, 10, 24, 12, 0, 0, 0, paris), paris)
	if err != nil {
		t.Fatal(err)
	}
	second, err := spec.Next(first, paris)
	if err != nil {
		t.Fatal(err)
	}
	if second.Sub(first) < 23*time.Hour {
		t.Errorf("fired at %s and again at %s: the repeated hour must fire once", first, second)
	}
}

func TestParseRefusesWhatIsNotASchedule(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *",
		"* * * 13 *", "* * * * 8", "*/0 * * * *", "5-1 * * * *", "a * * * *",
	} {
		if _, err := Parse(expression); err == nil {
			t.Errorf("Parse(%q) accepted it", expression)
		}
	}

	spec, err := Parse("0 0 30 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spec.Next(time.Now(), time.UTC); err != ErrNever {
		t.Errorf("the 30th of February: got %v, want ErrNever", err)
	}
}
