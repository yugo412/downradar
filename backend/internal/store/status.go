package store

// Status says whether a vendor is live. The suggest form writes a proposed row
// and a moderator publishes it; one no adapter could read is no_match, kept but
// not polled until a profile for its platform exists.
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
