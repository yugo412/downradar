package store

// Status says whether a vendor is live. A row written by the suggest form is
// proposed; a moderator publishes it later.
type Status string

const (
	StatusProposed  Status = "proposed"
	StatusPublished Status = "published"
	StatusRejected  Status = "rejected"
)

// Valid reports whether the value is one the database constraint accepts.
func (s Status) Valid() bool {
	switch s {
	case StatusProposed, StatusPublished, StatusRejected:
		return true
	default:
		return false
	}
}
