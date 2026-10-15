package store

import "time"

func nullify(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func nullInt(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}
