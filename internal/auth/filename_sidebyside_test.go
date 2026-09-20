package auth

import "testing"

func TestSideBySideFileName(t *testing.T) {
	// The same API key must produce a file name under our provider prefix,
	// distinct from the community plugin's file for the same key.
	key := "user_examplekey123"
	got := FileName(key)
	want := "command-code-" + Fingerprint(key) + ".json"
	if got != want {
		t.Fatalf("FileName = %q, want %q", got, want)
	}
	if got == "commandcode-bridge-"+Fingerprint(key)+".json" {
		t.Fatal("must not collide with the community plugin's credential file")
	}
	t.Logf("our credential file: %s", got)
}
