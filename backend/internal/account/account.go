// Package account sends the emails an account needs on its own: confirming an
// address, signing somebody in from their inbox, letting a forgotten password
// back in, and saying goodbye when the account is gone. It is not queued like
// alert mail, and it is about people, not vendors.
package account

import (
	"context"
	"fmt"
	"net/url"
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

// letter is what each of these emails is written with: who to greet, and the
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

// Login sends the link that signs somebody in without a password. It is the
// shortest lived of the three, because it opens the account itself. next is the
// page to open after, already checked as a site path and escaped here.
func (m *Mailer) Login(ctx context.Context, to, name, token, next string) error {
	link := fmt.Sprintf("%s/login?token=%s", m.publicURL, token)
	if next != "" {
		link += "&next=" + url.QueryEscape(next)
	}
	return m.send(ctx, to, "Your sign in link", "login", letter{Name: name, URL: link})
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

// Deleted tells somebody their account is gone. It is sent after the row has
// been removed, so it is the last message the address will get from the account
// and it has to say plainly that nothing can be brought back.
func (m *Mailer) Deleted(ctx context.Context, to, name string) error {
	return m.send(ctx, to, "Your DownRadar account has been deleted", "deleted", letter{
		Name: name,
		URL:  m.publicURL + "/contact",
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
