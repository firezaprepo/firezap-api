package auth

import (
	"strings"
	"testing"
)

func TestNewKeyIsRandomAndPrefixed(t *testing.T) {
	a, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewKey()
	if a == b {
		t.Fatal("two keys should never be equal")
	}
	if !strings.HasPrefix(a, "fzk_") || len(a) < 40 {
		t.Fatalf("unexpected key format: %q", a)
	}
}

func TestHashKeyIsStable(t *testing.T) {
	if HashKey("abc") != HashKey("abc") || HashKey("abc") == HashKey("abd") {
		t.Fatal("hash must be deterministic and distinct")
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":   "abc",
		"bearer  xyz ": "xyz",
		"Basic abc":    "",
		"":             "",
		"Bearer":       "",
	}
	for in, want := range cases {
		if got := BearerToken(in); got != want {
			t.Errorf("BearerToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidInstanceID(t *testing.T) {
	for _, ok := range []string{"my-number", "abc", "loja-01"} {
		if !ValidInstanceID(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"ab", "-abc", "abc-", "Abc", "a b c", "../etc", strings.Repeat("a", 41)} {
		if ValidInstanceID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
