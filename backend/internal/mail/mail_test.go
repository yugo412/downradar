package mail

import (
	"strings"
	"testing"
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
