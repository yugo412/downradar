package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMessageCarriesWhatAMailServerNeeds(t *testing.T) {
	msg := string(Message(
		"DownRadar <alerts@downradar.test>", "ann@example.com",
		"Example is now Degraded", "line one\nline two"))

	for _, want := range []string{
		"From: DownRadar <alerts@downradar.test>\r\n",
		"To: ann@example.com\r\n",
		"Subject: Example is now Degraded\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"\r\nline one\r\nline two",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestMessageKeepsAFreeTextSubjectOnOneLine(t *testing.T) {
	// A subject is built from a vendor name, which a moderator typed: a newline
	// in it would otherwise end the headers and let the rest be read as one.
	msg := string(Message("alerts@downradar.test", "ann@example.com",
		"Bad\r\nBcc: someone@example.com", "body"))

	if strings.Contains(msg, "\r\nBcc:") {
		t.Errorf("a newline in the subject split the headers:\n%s", msg)
	}
	if !strings.Contains(msg, "Subject: Bad Bcc: someone@example.com\r\n") {
		t.Errorf("subject was not folded onto one line:\n%s", msg)
	}
}

func TestEnvelopeIsABareAddress(t *testing.T) {
	cases := map[string]string{
		"DownRadar <alerts@downradar.test>": "alerts@downradar.test",
		"alerts@downradar.test":             "alerts@downradar.test",
		// A server rejects anything else here, and net/smtp has no opinion, so
		// what cannot be parsed is passed on as it came and fails visibly.
		"not an address": "not an address",
	}

	for from, want := range cases {
		if got := envelope(from); got != want {
			t.Errorf("envelope(%q) = %q, want %q", from, got, want)
		}
	}
}

func TestMessageEncodesANonASCIIHeader(t *testing.T) {
	msg := string(Message("alerts@downradar.test", "ann@example.com",
		"Café is now Degraded", "body"))

	if !strings.Contains(msg, "Subject: =?utf-8?") {
		t.Errorf("an accented subject went out raw:\n%s", msg)
	}
}

func TestImplicitTLSIsTheConventionForPort465(t *testing.T) {
	if !implicitTLS("465") {
		t.Error("port 465 is not dialled with TLS")
	}
	for _, port := range []string{"25", "587", "1025", "2525"} {
		if implicitTLS(port) {
			t.Errorf("port %s should be dialled plainly and upgraded with STARTTLS", port)
		}
	}
}

// TestSendDeliversOverAPlainConnection runs the conversation against a small
// server that says the right things, which is the only way to know the client
// still speaks SMTP after not handing it to smtp.SendMail.
func TestSendDeliversOverAPlainConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var mu sync.Mutex
	var transcript []string
	record := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		transcript = append(transcript, line)
	}
	done := make(chan struct{})

	go func() {
		defer close(done)

		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
		write("220 test ready")

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			record(line)

			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				write("250 test")
			case line == "DATA":
				write("354 go ahead")
				for {
					body, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimRight(body, "\r\n") == "." {
						break
					}
					record(body)
				}
				write("250 queued")
			case line == "QUIT":
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	}()

	sender := NewSMTP(ln.Addr().String(), "DownRadar <alerts@example.com>", "", "", time.Second)
	if err := sender.Send(context.Background(), "ann@example.com", "Hello", "body"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-done

	mu.Lock()
	got := strings.Join(transcript, "\n")
	mu.Unlock()

	for _, want := range []string{
		"MAIL FROM:<alerts@example.com>",
		"RCPT TO:<ann@example.com>",
		"Subject: Hello",
		"body",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript does not contain %q:\n%s", want, got)
		}
	}
}
