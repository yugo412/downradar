package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

type contactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// The lengths are generous for what a person would actually write, and small
// enough that nothing enormous reaches the mail server.
const (
	maxContactName    = 100
	maxContactSubject = 200
	maxContactBody    = 5000
)

// contact takes a message for the people who run the site and mails it to them.
// Every field is required: an address that is missing cannot be answered, and a
// subject is what makes the message findable again.
func (h *handlers) contact(w http.ResponseWriter, r *http.Request) {
	if h.contactMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the contact form is not available right now",
		})
		return
	}

	var body contactRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	name := clean(sanitize(body.Name))
	subject := clean(sanitize(body.Subject))
	message := strings.TrimSpace(contactBody(body.Body))

	email, problem := parseEmail(body.Email)
	if problem == "" {
		problem = contactProblem(name, subject, message)
	}
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	if err := h.contactMailer.Send(r.Context(), name, email, subject, message); err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("contact message sent", "from", email)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "sent"})
}

// contactProblem is what is wrong with the rest of the form, in the order a
// person fills it in.
func contactProblem(name, subject, body string) string {
	switch {
	case name == "":
		return "a name is required"
	case utf8.RuneCountInString(name) > maxContactName:
		return "that name is too long"
	case subject == "":
		return "a subject is required"
	case utf8.RuneCountInString(subject) > maxContactSubject:
		return "that subject is too long"
	case body == "":
		return "a message is required"
	case utf8.RuneCountInString(body) > maxContactBody:
		return "that message is too long"
	}
	return ""
}

// contactBody keeps the line breaks a person typed, which sanitize would fold
// into spaces, while still dropping the control characters that have no place in
// an email.
func contactBody(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")

	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}
