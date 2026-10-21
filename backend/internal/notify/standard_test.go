package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/store"
)

// verifiedWith is what a receiver does on its own: sign the message it was given
// with the secret it kept, and see whether the signature that arrived is one of
// them. Returning the one it could read back is what lets a test say which
// secret a signature belongs to.
func verifiedWith(secret, id, timestamp, body string, header string) bool {
	key, err := store.WebhookSigningKey(secret)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "." + body))
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	for _, got := range strings.Split(header, " ") {
		if hmac.Equal([]byte(got), []byte(want)) {
			return true
		}
	}
	return false
}

// A signed call is what the Standard Webhooks specification says it is: an id,
// a timestamp, and one signature over both and the body, made with the secret the
// receiver also has.
func TestASignedCallCarriesWhatTheSpecificationSays(t *testing.T) {
	st := testStore(t)
	server, calls := newReceiver(t, http.StatusNoContent)

	secret, err := store.NewWebhookSecret()
	if err != nil {
		t.Fatalf("NewWebhookSecret: %v", err)
	}
	slug := webhookVendor(t, st, nil, store.AlertWebhook{
		URL:    server.URL,
		Method: http.MethodPost,
		Auth:   store.WebhookAuthStandard,
		Signing: store.WebhookSigning{
			Secret: secret,
		},
	})
	if slug == "" {
		t.Fatal("no vendor")
	}
	// The fake sender takes the emails; a call never goes near a mail server.
	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	call := calls.last(t)
	id := call.headers.Get("webhook-id")
	timestamp := call.headers.Get("webhook-timestamp")
	signature := call.headers.Get("webhook-signature")

	if !strings.HasPrefix(id, "msg_") {
		t.Errorf("webhook-id = %q, want a message id", id)
	}
	if timestamp == "" {
		t.Error("the call carries no timestamp")
	}
	if !verifiedWith(secret, id, timestamp, call.body, signature) {
		t.Errorf("webhook-signature = %q does not verify with the secret", signature)
	}
	if strings.Count(signature, "v1,") != 1 {
		t.Errorf("webhook-signature = %q, want one signature when nothing is being rotated", signature)
	}

	// The body is still the one the subscriber reads, and the headers they wrote
	// are still there beside the signature.
	var payload receivedPayload
	if err := json.Unmarshal([]byte(call.body), &payload); err != nil {
		t.Fatalf("body = %q, want JSON: %v", call.body, err)
	}
	if payload.Changed.To != "major" {
		t.Errorf("changed.to = %q, want the change that earned the call", payload.Changed.To)
	}
}

// A rotation is two signatures in the same header for a day: a receiver that has
// the old secret and one that has the new one can both verify the call.
func TestARotationSignsWithBothSecrets(t *testing.T) {
	oldSecret, err := store.NewWebhookSecret()
	if err != nil {
		t.Fatalf("NewWebhookSecret: %v", err)
	}
	newSecret, err := store.NewWebhookSecret()
	if err != nil {
		t.Fatalf("NewWebhookSecret: %v", err)
	}

	hook := store.AlertWebhook{
		Auth: store.WebhookAuthStandard,
		Signing: store.WebhookSigning{
			Secret:        newSecret,
			Previous:      oldSecret,
			PreviousUntil: time.Now().Add(time.Hour),
		},
	}

	headers, err := standardHeaders(hook, "msg_1", `{"a":1}`, time.Now())
	if err != nil {
		t.Fatalf("standardHeaders: %v", err)
	}
	byName := map[string]string{}
	for _, header := range headers {
		byName[strings.ToLower(header.Name)] = header.Value
	}
	signature := byName["webhook-signature"]
	if strings.Count(signature, "v1,") != 2 {
		t.Fatalf("webhook-signature = %q, want both secrets while one is still going out", signature)
	}
	if !verifiedWith(newSecret, byName["webhook-id"], byName["webhook-timestamp"], `{"a":1}`, signature) {
		t.Error("the new secret does not verify the call")
	}
	if !verifiedWith(oldSecret, byName["webhook-id"], byName["webhook-timestamp"], `{"a":1}`, signature) {
		t.Error("the retired secret does not verify the call during the change over")
	}

	// Once the day is up the old one is not signed with any more.
	hook.Signing.PreviousUntil = time.Now().Add(-time.Minute)
	headers, err = standardHeaders(hook, "msg_1", `{"a":1}`, time.Now())
	if err != nil {
		t.Fatalf("standardHeaders: %v", err)
	}
	signature = headers[len(headers)-1].Value
	if strings.Count(signature, "v1,") != 1 {
		t.Errorf("webhook-signature = %q, want only the secret that is set", signature)
	}
}

// The three header names belong to the sender: a subscriber who writes one is
// not sending it themselves.
func TestTheSignatureIsNotSomethingASubscriberCanWrite(t *testing.T) {
	signed := []store.AlertField{
		{Name: "webhook-id", Value: "msg_1"},
		{Name: "webhook-timestamp", Value: "1700000000"},
		{Name: "webhook-signature", Value: "v1,real"},
	}
	theirs := []store.AlertField{
		{Name: "WEBHOOK-SIGNATURE", Value: "v1,mine"},
		{Name: "X-Source", Value: "downradar"},
	}

	headers := replaceHeaders(theirs, signed)
	signatures := 0
	for _, header := range headers {
		if strings.EqualFold(header.Name, "webhook-signature") {
			signatures++
			if header.Value != "v1,real" {
				t.Errorf("webhook-signature = %q, want the sender's", header.Value)
			}
		}
	}
	if signatures != 1 {
		t.Errorf("the call carries %d signature headers, want one", signatures)
	}
	if len(headers) != len(signed)+1 {
		t.Errorf("headers = %+v, want the other one kept beside the sender's", headers)
	}
}
