package account

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type spy struct {
	mu       sync.Mutex
	messages []message
}

type message struct {
	to      string
	subject string
	body    string
}

func (s *spy) Send(ctx context.Context, to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message{to: to, subject: subject, body: body})
	return nil
}

func TestVerifyAndResetCarryTheirOwnLinks(t *testing.T) {
	s := &spy{}
	m := New(s, "https://downradar.info/")

	ctx := context.Background()
	if err := m.Verify(ctx, "ann@example.com", "Ann", "tok-verify"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := m.Reset(ctx, "ann@example.com", "Ann", "tok-reset"); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if len(s.messages) != 2 {
		t.Fatalf("messages = %d, want two", len(s.messages))
	}
	verify, reset := s.messages[0], s.messages[1]

	// The trailing slash on the site address must not double up.
	if !strings.Contains(verify.body, "https://downradar.info/verify?token=tok-verify") {
		t.Errorf("the confirmation link is wrong:\n%s", verify.body)
	}
	if !strings.Contains(reset.body, "https://downradar.info/reset?token=tok-reset") {
		t.Errorf("the reset link is wrong:\n%s", reset.body)
	}
	for _, m := range s.messages {
		if m.to != "ann@example.com" {
			t.Errorf("message went to %q", m.to)
		}
		if !strings.Contains(m.body, "Ann") {
			t.Errorf("the message does not greet the person:\n%s", m.body)
		}
	}
	if verify.subject == reset.subject {
		t.Errorf("both messages have the subject %q", verify.subject)
	}
}
