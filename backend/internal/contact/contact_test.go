package contact

import (
	"context"
	"strings"
	"testing"
)

type spy struct {
	to      string
	subject string
	body    string
	replyTo string
}

func (s *spy) SendReply(_ context.Context, to, subject, body, replyTo string) error {
	s.to, s.subject, s.body, s.replyTo = to, subject, body, replyTo
	return nil
}

func TestSendGoesToTheOwnerAndAnswersToTheSender(t *testing.T) {
	s := &spy{}
	m := New(s, "owner@example.com")

	if err := m.Send(context.Background(), "Ann", "ann@example.com", "Hello there", "line one\nline two"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if s.to != "owner@example.com" {
		t.Errorf("to = %q, want the owner's address", s.to)
	}
	if s.replyTo != "ann@example.com" {
		t.Errorf("replyTo = %q, want the sender, so answering is a reply", s.replyTo)
	}
	if !strings.Contains(s.subject, "Hello there") {
		t.Errorf("subject = %q, want the one that was typed", s.subject)
	}
	if !strings.Contains(s.body, "ann@example.com") || !strings.Contains(s.body, "line two") {
		t.Errorf("body = %q, want the sender and the message", s.body)
	}
}
