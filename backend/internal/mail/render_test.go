package mail

import (
	"strings"
	"testing"
)

// Every message the site sends is set in the same shell, so the brand and the
// surface are the same in all of them and a template only says its own part.
func TestEveryMessageIsSetInTheShell(t *testing.T) {
	cases := map[string]any{
		"verify":  map[string]string{"Name": "Ann", "URL": "https://downradar.info/verify?token=tok"},
		"reset":   map[string]string{"Name": "Ann", "URL": "https://downradar.info/reset?token=tok"},
		"deleted": map[string]string{"Name": "Ann", "URL": "https://downradar.info/contact"},
		"contact": map[string]string{"Name": "Ann", "Email": "ann@example.com", "Subject": "Hello", "Body": "line one\nline two"},
		"alert": map[string]any{
			"VendorName": "Example", "ComponentName": "Search",
			"What":        "Search on Example",
			"From":        map[string]string{"Label": "Operational", "Color": "#007f43", "Ink": "#ffffff"},
			"To":          map[string]string{"Label": "Degraded", "Color": "#ee8134", "Ink": "#ffffff"},
			"ChangedAt":   "2026-10-17 08:15 UTC",
			"URL":         "https://downradar.info/example",
			"Unsubscribe": "https://downradar.info/alerts/unsubscribe?token=tok",
		},
	}

	for name, data := range cases {
		body, err := Render(name, data)
		if err != nil {
			t.Fatalf("Render(%s): %v", name, err)
		}

		for _, want := range []string{"<!doctype html>", "DownRadar", "Official status pages, aggregated"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not contain %q:\n%s", name, want, body)
			}
		}

		// A colour written into a style attribute is a value a template can
		// refuse, and a refused one comes out as ZgotmplZ rather than as a
		// colour. It would be invisible in a test that only looked for words.
		if strings.Contains(body, "ZgotmplZ") {
			t.Errorf("%s has a value the template refused:\n%s", name, body)
		}
	}
}

// What a visitor typed is text, not markup: a name from the form must not be
// able to put anything of its own into the email.
func TestRenderEscapesWhatCameFromAForm(t *testing.T) {
	body, err := Render("contact", map[string]string{
		"Name":    "<script>alert(1)</script>",
		"Email":   "ann@example.com",
		"Subject": "Hello",
		"Body":    "<b>bold</b>",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if strings.Contains(body, "<script>") || strings.Contains(body, "<b>bold</b>") {
		t.Errorf("markup from the form reached the email:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("the name is not in the email at all:\n%s", body)
	}
}

// A message that was typed over several lines keeps its line breaks, because
// they are part of what was written.
func TestNl2brKeepsTheLinesAndEscapesThem(t *testing.T) {
	got := string(nl2br("line one\nline two & <three>\r\nfour"))

	want := "line one<br />line two &amp; &lt;three&gt;<br />four"
	if got != want {
		t.Errorf("nl2br = %q, want %q", got, want)
	}
}

func TestRenderRefusesATemplateThatDoesNotExist(t *testing.T) {
	if _, err := Render("no-such-message", nil); err == nil {
		t.Error("a missing template was rendered rather than refused")
	}
}
