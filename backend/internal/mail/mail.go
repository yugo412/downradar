// Package mail sends one plain text message over SMTP. It knows nothing about
// alerts: it is the transport, and the notifier decides what to say.
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// SMTP is a mail server. The zero value of every optional field means "do not
// use it": a server that takes mail without a login is the normal case in
// development, and STARTTLS is used whenever the server offers it.
type SMTP struct {
	addr     string
	from     string
	user     string
	password string
	timeout  time.Duration
}

func NewSMTP(addr, from, user, password string, timeout time.Duration) *SMTP {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &SMTP{addr: addr, from: from, user: user, password: password, timeout: timeout}
}

// Send delivers one message. The context bounds the whole conversation, because
// net/smtp has no notion of one.
func (s *SMTP) Send(ctx context.Context, to, subject, body string) error {
	host, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		return fmt.Errorf("mail: parse address %q: %w", s.addr, err)
	}

	var auth smtp.Auth
	if s.user != "" {
		auth = smtp.PlainAuth("", s.user, s.password, host)
	}

	done := make(chan error, 1)
	go func() {
		done <- send(s.addr, host, port, auth, envelope(s.from), to,
			Message(s.from, to, subject, body))
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("mail: send to %s: %w", to, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// send is the conversation itself. It is written out rather than handed to
// smtp.SendMail for one reason: a server on 465 speaks TLS from the first byte,
// and SendMail dials in the clear and waits for a greeting, which such a server
// never sends in the clear. Every other port is dialled plainly and upgraded
// with STARTTLS when the server offers it, which is what SendMail would have
// done anyway.
func send(addr, host, port string, auth smtp.Auth, from, to string, msg []byte) error {
	var (
		conn net.Conn
		err  error
	)
	if implicitTLS(port) {
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if !implicitTLS(port) {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return err
			}
		}
	}

	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return err
			}
		}
	}

	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}

	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	return client.Quit()
}

// implicitTLS reports whether a server speaks TLS from the first byte. That is
// what port 465 means, and it is the convention every mail tool follows.
func implicitTLS(port string) bool {
	return port == "465"
}

// envelope is the address a mail server is given as the sender. A display name
// belongs in the header and nowhere else: servers reject "Name <addr>" here,
// which is exactly what Mailpit did.
func envelope(from string) string {
	parsed, err := mail.ParseAddress(from)
	if err != nil {
		return from
	}
	return parsed.Address
}

// Message builds the RFC 5322 bytes of one message. It is separate from sending
// so it can be read and tested on its own, and so the headers cannot depend on
// the mail server.
func Message(from, to, subject, body string) []byte {
	var b strings.Builder

	b.WriteString("From: " + header(from) + "\r\n")
	b.WriteString("To: " + header(to) + "\r\n")
	b.WriteString("Subject: " + header(subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")

	// SMTP is line based and CRLF terminated, so a bare newline in the body has
	// to be normalised before it goes on the wire.
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))

	return []byte(b.String())
}

// header keeps a value on one line and inside ASCII. A subject is built from a
// vendor name, which is free text a moderator typed: a newline in it would
// otherwise split the headers, and an accent has to be encoded rather than sent
// raw.
func header(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if isASCII(value) {
		return value
	}
	return mime.QEncoding.Encode("utf-8", value)
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] > 127 {
			return false
		}
	}
	return true
}
