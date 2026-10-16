// Package account sends the two emails an account needs on its own: one that
// proves an address belongs to whoever typed it, and one that lets somebody who
// has forgotten their password back in. It is separate from the alert mailer
// because it is about people rather than about vendors, and because these are
// sent while the request is being answered, not queued.
package account

import (
	"context"
	"fmt"
	"strings"
)

// Sender delivers one message. The SMTP mailer is the real one; tests pass a
// fake, so the wording can be read without a server.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

type Mailer struct {
	sender    Sender
	publicURL string
}

func New(sender Sender, publicURL string) *Mailer {
	return &Mailer{sender: sender, publicURL: strings.TrimSuffix(publicURL, "/")}
}

// Verify sends the link that proves the address. The token travels in the query
// string of a page on the site, which then posts it back: a link that acted on
// its own would be spent by the first mail scanner that fetched it.
func (m *Mailer) Verify(ctx context.Context, to, name, token string) error {
	subject := "Confirm your email address"

	body := fmt.Sprintf(`Hello %s,

Confirm this address to finish setting up your account:

%s/verify?token=%s

Until then, your account works but we have no way to know the address is yours.
If you did not make an account, you can ignore this email.
`, name, m.publicURL, token)

	return m.sender.Send(ctx, to, subject, body)
}

// Reset sends the link that lets somebody choose a new password. It is shorter
// lived than the other one, because a working password reset in an old inbox is
// worth more to a stranger than an unconfirmed address.
func (m *Mailer) Reset(ctx context.Context, to, name, token string) error {
	subject := "Reset your password"

	body := fmt.Sprintf(`Hello %s,

Someone asked to reset the password for this address. If it was you, choose a
new one here:

%s/reset?token=%s

The link works once and expires in an hour. If it was not you, nothing has
changed and you can ignore this email.
`, name, m.publicURL, token)

	return m.sender.Send(ctx, to, subject, body)
}
