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

	"downradar.info/backend/internal/mail"
)

// Sender delivers one message. The SMTP mailer is the real one; tests pass a
// fake, so the message can be read without a server.
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

// letter is what both of these emails are written with: who to greet, and the
// link that does the work. The wording around them is in the templates.
type letter struct {
	Name string
	URL  string
}

// Verify sends the link that proves the address. The token travels in the query
// string of a page on the site, which then posts it back: a link that acted on
// its own would be spent by the first mail scanner that fetched it.
func (m *Mailer) Verify(ctx context.Context, to, name, token string) error {
	return m.send(ctx, to, "Confirm your email address", "verify", letter{
		Name: name,
		URL:  fmt.Sprintf("%s/verify?token=%s", m.publicURL, token),
	})
}

// Reset sends the link that lets somebody choose a new password. It is shorter
// lived than the other one, because a working password reset in an old inbox is
// worth more to a stranger than an unconfirmed address.
func (m *Mailer) Reset(ctx context.Context, to, name, token string) error {
	return m.send(ctx, to, "Reset your password", "reset", letter{
		Name: name,
		URL:  fmt.Sprintf("%s/reset?token=%s", m.publicURL, token),
	})
}

// send writes one of them from its template and hands it to the mail server. A
// template that cannot be filled is a mistake in the code, and it is answered as
// an error rather than sent as an empty message.
func (m *Mailer) send(ctx context.Context, to, subject, template string, data any) error {
	body, err := mail.Render(template, data)
	if err != nil {
		return err
	}
	return m.sender.Send(ctx, to, subject, body)
}
