package store

// Viewer is who is asking for a service. The zero value is the public, which
// sees only the catalogue. IsStaff is decided by the caller, because knowing
// which roles count as staff is an authentication question, not a storage one.
type Viewer struct {
	UserID  int64
	IsStaff bool
}

// The two answers to who a service is for, kept apart from its status.
const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

// Public is the viewer used by the open API.
func Public() Viewer {
	return Viewer{}
}

func (v Viewer) Owns(ownerID *int64) bool {
	return ownerID != nil && v.UserID != 0 && v.UserID == *ownerID
}
