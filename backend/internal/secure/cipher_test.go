package secure

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	c, err := New("a master value")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sealed, err := c.Seal([]byte(`{"botToken":"123:secret"}`))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, "secret") {
		t.Fatal("the sealed value still carries its plaintext")
	}

	plain, err := c.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(plain) != `{"botToken":"123:secret"}` {
		t.Errorf("plain = %q", plain)
	}
}

func TestCipherRejectsATamperedValue(t *testing.T) {
	c, err := New("a master value")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sealed, err := c.Seal([]byte("the secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Changing one byte of the ciphertext is enough to fail the tag.
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("decode the sealed value: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	if _, err := c.Open(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("a changed value was opened")
	}
}

func TestCipherRejectsAnotherKey(t *testing.T) {
	first, err := New("the first master")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	second, err := New("the second master")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sealed, err := first.Seal([]byte("the secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := second.Open(sealed); err == nil {
		t.Fatal("a value was opened with another key")
	}
}

func TestNewRejectsAnEmptyMaster(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("an empty master was accepted")
	}
}
