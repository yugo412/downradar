package store

// Status says whether a vendor is live. A row written by the suggest form is
// proposed; a moderator publishes it later. A vendor added without a profile
// that no adapter could read is no_match: it is kept, but not polled until a
// profile for its platform exists.
type Status string

const (
	StatusProposed  Status = "proposed"
	StatusPublished Status = "published"
	StatusRejected  Status = "rejected"
	StatusNoMatch   Status = "no_match"
)

// Valid reports whether the value is one the database constraint accepts.
func (s Status) Valid() bool {
	switch s {
	case StatusProposed, StatusPublished, StatusRejected, StatusNoMatch:
		return true
	default:
		return false
	}
}
