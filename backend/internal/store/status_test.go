package store

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Strava":                "strava",
		"Some Service":          "some-service",
		"  padded  ":            "padded",
		"multi   spaces":        "multi-spaces",
		"Already-Slugged":       "already-slugged",
		"Trailing dash -":       "trailing-dash",
		"Punctuation!@#$%^&*()": "punctuation",
		"Numbers 123":           "numbers-123",
		"Café":                  "café",
		"日本語":                   "日本語",
		"":                      "",
		"---":                   "",
		"a/b\\c":                "a-b-c",
	}

	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugifyCapsLength(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "a"
	}
	if got := Slugify(long); len(got) > maxSlugLength {
		t.Errorf("slug length = %d, want at most %d", len(got), maxSlugLength)
	}
}

func TestStatusValid(t *testing.T) {
	for _, s := range []Status{StatusProposed, StatusPublished, StatusRejected} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []Status{"", "draft", "PUBLISHED"} {
		if s.Valid() {
			t.Errorf("%q should not be valid", s)
		}
	}
}
