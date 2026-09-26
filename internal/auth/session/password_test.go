package session

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTripAndSalt(t *testing.T) {
	const password = "a sufficiently long password"
	first, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("salt reused")
	}
	if !VerifyPassword(first, password) || VerifyPassword(first, password+"x") {
		t.Fatal("incorrect password verification")
	}
	if VerifyPassword("eggy-pbkdf2-sha256-v1$999999999$x$y", password) {
		t.Fatal("unbounded work factor accepted")
	}
}

func TestValidatePasswordBounds(t *testing.T) {
	cases := []struct {
		name     string
		password string
		ok       bool
	}{
		{"eleven bytes", strings.Repeat("a", 11), false},
		{"twelve bytes", strings.Repeat("a", 12), true},
		{"256 bytes", strings.Repeat("a", 256), true},
		{"257 bytes", strings.Repeat("a", 257), false},
		{"invalid utf-8", "valid-prefix\xff\xfe-suffix", false},
		{"whitespace preserved", "   padded password   ", true},
		{"multibyte counts bytes", strings.Repeat("é", 6), true}, // 12 bytes
		{"multibyte short", strings.Repeat("é", 5), false},       // 10 bytes
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidatePassword(%q) err=%v want ok=%v", tc.password, err, tc.ok)
			}
			if _, err := HashPassword(tc.password); (err == nil) != tc.ok {
				t.Fatalf("HashPassword(%q) err=%v want ok=%v", tc.password, err, tc.ok)
			}
		})
	}
}

func TestVerifyPasswordPreservesWhitespace(t *testing.T) {
	encoded, err := HashPassword("  spaced password  ")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(encoded, "  spaced password  ") {
		t.Fatal("exact password rejected")
	}
	if VerifyPassword(encoded, "spaced password") {
		t.Fatal("trimmed password accepted")
	}
}

func TestVerifyPasswordRejectsMalformedEncodings(t *testing.T) {
	const password = "a sufficiently long password"
	good, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(good, "$")
	if len(parts) != 4 {
		t.Fatalf("segments=%d", len(parts))
	}
	join := func(p ...string) string { return strings.Join(p, "$") }
	cases := map[string]string{
		"empty":                "",
		"wrong algorithm":      join("eggy-scrypt-v1", parts[1], parts[2], parts[3]),
		"wrong version":        join("eggy-pbkdf2-sha256-v2", parts[1], parts[2], parts[3]),
		"low iterations":       join(parts[0], "1000", parts[2], parts[3]),
		"padded iterations":    join(parts[0], "0600000", parts[2], parts[3]),
		"malformed salt":       join(parts[0], parts[1], "not*base64", parts[3]),
		"malformed key":        join(parts[0], parts[1], parts[2], "not*base64"),
		"short salt":           join(parts[0], parts[1], "AAAA", parts[3]),
		"short key":            join(parts[0], parts[1], parts[2], "AAAA"),
		"extra segment":        good + "$extra",
		"missing segment":      join(parts[0], parts[1], parts[2]),
		"embedded dollar salt": join(parts[0], parts[1], parts[2]+"$", parts[3][:len(parts[3])-1]),
		"padded base64":        join(parts[0], parts[1], parts[2], parts[3]+"="),
	}
	for name, encoded := range cases {
		if VerifyPassword(encoded, password) {
			t.Errorf("%s: malformed encoding %q verified", name, encoded)
		}
	}
	if VerifyPassword(good, "valid-prefix\xff\xfe-suffix") {
		t.Fatal("invalid utf-8 password verified")
	}
	if VerifyPassword(good, strings.Repeat("a", 257)) {
		t.Fatal("overlong password verified")
	}
}

func TestHashEnvironmentPasswordAllowsShortSecrets(t *testing.T) {
	encoded, err := HashEnvironmentPassword("short")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(encoded, "short") || VerifyPassword(encoded, "shorter") {
		t.Fatal("environment hash verification wrong")
	}
	if _, err := HashEnvironmentPassword(""); err == nil {
		t.Fatal("empty environment password accepted")
	}
	if _, err := HashEnvironmentPassword(strings.Repeat("a", 257)); err == nil {
		t.Fatal("overlong environment password accepted")
	}
	if _, err := HashEnvironmentPassword("bad\xff"); err == nil {
		t.Fatal("invalid utf-8 environment password accepted")
	}
}

func BenchmarkVerifyPassword(b *testing.B) {
	const password = "a sufficiently long password"
	encoded, err := HashPassword(password)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if !VerifyPassword(encoded, password) {
			b.Fatal("verification failed")
		}
	}
}
