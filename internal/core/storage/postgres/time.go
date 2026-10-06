package postgres

import "time"

// nullableTime turns a zero time into a SQL NULL, so that a column default such
// as now() applies instead of a zero timestamp.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
