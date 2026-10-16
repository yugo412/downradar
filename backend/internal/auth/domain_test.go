package auth

import "testing"

func TestOnReservedDomain(t *testing.T) {
	cases := map[string]bool{
		"someone@downradar.info":      true,
		"someone@DOWNRADAR.INFO":      true,
		"someone@mail.downradar.info": true,
		"someone@notdownradar.info":   false,
		"someone@downradar.info.evil": false,
		"someone@example.com":         false,
		"no-at-sign":                  false,
		"":                            false,
	}

	for email, want := range cases {
		if got := OnReservedDomain(email); got != want {
			t.Errorf("OnReservedDomain(%q) = %t, want %t", email, got, want)
		}
	}
}
