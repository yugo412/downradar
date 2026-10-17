package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestContactNeedsEveryField(t *testing.T) {
	_, router := testRouter(t)

	for _, body := range []string{
		`{"email":"ann@example.com","subject":"Hello","body":"A message."}`,
		`{"name":"Ann","subject":"Hello","body":"A message."}`,
		`{"name":"Ann","email":"not an address","subject":"Hello","body":"A message."}`,
		`{"name":"Ann","email":"ann@example.com","body":"A message."}`,
		`{"name":"Ann","email":"ann@example.com","subject":"Hello"}`,
	} {
		rec := call(t, router, http.MethodPost, "/api/v1/contact", body, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestContactMailsTheOwnerAndAnswersToTheSender(t *testing.T) {
	_, router, spy := testRouterWithMail(t)

	rec := call(t, router, http.MethodPost, "/api/v1/contact",
		`{"name":"Ann","email":"Ann@Example.com","subject":"Hello there","body":"Line one.\nLine two."}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	sent := spy.sent()
	if len(sent) != 1 {
		t.Fatalf("messages = %d, want one", len(sent))
	}
	msg := sent[0]
	if msg.to != "owner@downradar.test" {
		t.Errorf("to = %q, want the owner address", msg.to)
	}
	if msg.replyTo != "ann@example.com" {
		t.Errorf("replyTo = %q, want the sender, normalised", msg.replyTo)
	}
	if !strings.Contains(msg.subject, "Hello there") {
		t.Errorf("subject = %q, want the one that was typed", msg.subject)
	}
	// A message is written in paragraphs, so the line breaks have to survive.
	// They are markup in the email, which is what makes them survive there.
	if !strings.Contains(msg.body, "Line one.<br />Line two.") {
		t.Errorf("body = %q, want the line breaks kept", msg.body)
	}
}
