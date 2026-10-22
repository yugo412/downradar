// Package oauth signs somebody in through a provider that already knows who they
// are. The site never sees a password, and only the exchange is per provider: the
// routes, the account lookup and the session are written once in the handler.
package oauth

import "context"

// Identity is what a provider says about the person it just sent back: the
// account it knows, the address it has proved, and what it calls them.
type Identity struct {
	// Subject is the provider's own id for the person. It is what the link
	// between an account and a provider is stored by, because it stays the same
	// when the address or a username changes.
	Subject string
	// Email is an address the provider has verified, so an account made from it
	// is already confirmed.
	Email string
	// Name is the display name, which a provider may not have.
	Name string
	// Username is the name the provider knows the person by, and the fallback
	// for a display name.
	Username string
}

// Provider is one way to sign in.
type Provider interface {
	// Name is both the path segment the routes use and the name the link is
	// stored under, so it must not change once accounts are linked with it.
	Name() string
	// Label is what the button on the sign in screens says.
	Label() string
	// AuthorizeURL is where the browser is sent to start the flow.
	AuthorizeURL(state, redirectURI string) string
	// Exchange turns the code the provider sent back into the identity behind
	// it. An address the provider has not verified is not an identity: the code
	// that calls this will not link an account to an unproved one.
	Exchange(ctx context.Context, code, redirectURI string) (Identity, error)
}
