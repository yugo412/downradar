// Package contact sends what a visitor typed on the contact form to the address
// the site is run from. The reply address is set to the sender, so answering is a
// reply rather than a copy and paste. It is separate from the alert and account
// mailers because it is about people writing in, not about vendors or accounts.
package contact

import (
	"context"

	"downradar.info/backend/internal/mail"
)

// Sender delivers one message with a reply address. The SMTP mailer is the real
// one; tests pass a fake, so the message can be read without a server.
type Sender interface {
	SendReply(ctx context.Context, to, subject, body, replyTo string) error
}

type Mailer struct {
	sender Sender
	owner  string
}

func New(sender Sender, owner string) *Mailer {
	return &Mailer{sender: sender, owner: owner}
}

// letter is what the email is written with: who wrote in, what they called it,
// and what they typed. The wording around them is in the template.
type letter struct {
	Name    string
	Email   string
	Subject string
	Body    string
}

// Send delivers one message from the form. The subject is prefixed so it stands
// out among the alert and account mail the same inbox receives, and the body
// names the sender so the address is visible without opening the headers.
func (m *Mailer) Send(ctx context.Context, name, email, subject, body string) error {
	html, err := mail.Render("contact", letter{
		Name:    name,
		Email:   email,
		Subject: subject,
		Body:    body,
	})
	if err != nil {
		return err
	}
	return m.sender.SendReply(ctx, m.owner, "Contact: "+subject, html, email)
}
