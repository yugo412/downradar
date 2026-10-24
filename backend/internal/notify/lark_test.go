package notify

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

// A Lark call is a card with the status colour, and a signed one carries the
// timestamp and signature the custom bot checks. The code in the answer decides
// whether the message arrived, since Lark reports refusals in the body.
func TestLarkCallSignsAndReadsTheCode(t *testing.T) {
	st := testStore(t)
	serviceID, _, _ := snapshotVendor(t, st)
	ctx := context.Background()

	server, calls := newReceiver(t, 200)
	calls.answerWith(`{"code":0,"msg":"success"}`)

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	result, err := n.TestLark(ctx, serviceID, server.URL, "a-secret")
	if err != nil {
		t.Fatalf("TestLark: %v", err)
	}
	if !result.OK || result.Code != 0 {
		t.Fatalf("result = %+v, want a delivered message", result)
	}

	got := calls.last(t)
	if agent := got.headers.Get("User-Agent"); agent != "DownRadar (+https://downradar.info)" {
		t.Errorf("User-Agent = %q, want the site's own", agent)
	}

	var payload struct {
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
		MsgType   string `json:"msg_type"`
		Card      struct {
			Header struct {
				Template string `json:"template"`
			} `json:"header"`
		} `json:"card"`
	}
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("decode the card: %v", err)
	}
	if payload.MsgType != "interactive" {
		t.Errorf("msg_type = %q, want interactive", payload.MsgType)
	}
	if payload.Card.Header.Template != "red" {
		t.Errorf("header template = %q, want red for a major change", payload.Card.Header.Template)
	}
	timestamp, err := strconv.ParseInt(payload.Timestamp, 10, 64)
	if err != nil {
		t.Fatalf("timestamp = %q: %v", payload.Timestamp, err)
	}
	if payload.Sign != larkSign("a-secret", timestamp) {
		t.Errorf("sign = %q, want the signature of the timestamp and secret", payload.Sign)
	}

	// Lark can answer 200 and still refuse in the body; the code is what says so.
	calls.answerWith(`{"code":19021,"msg":"sign match fail or timestamp is not within one hour from current time"}`)
	result, err = n.TestLark(ctx, serviceID, server.URL, "a-secret")
	if err != nil {
		t.Fatalf("TestLark refusal: %v", err)
	}
	if result.OK || result.Code != 19021 || result.Error == "" {
		t.Errorf("result = %+v, want the code and the refusal shown", result)
	}
}
