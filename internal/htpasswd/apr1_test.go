package htpasswd

import (
	"strings"
	"testing"
)

func TestHashWithSaltMatchesOpenSSL(t *testing.T) {
	cases := []struct {
		password, salt, want string
	}{
		{"s3cret!", "abcd1234", "$apr1$abcd1234$IkGKNxdNZbte197wteusK0"},
		{"hello world", "Xy9zAbcd", "$apr1$Xy9zAbcd$r4md7RU8G8K.wEBO1Qbiy1"},
	}
	for _, c := range cases {
		if got := hashWithSalt(c.password, c.salt); got != c.want {
			t.Errorf("hashWithSalt(%q, %q) = %q, want %q", c.password, c.salt, got, c.want)
		}
	}
}

func TestHashFormat(t *testing.T) {
	h := Hash("password")
	if !strings.HasPrefix(h, "$apr1$") {
		t.Errorf("hash %q missing apr1 prefix", h)
	}
	if parts := strings.Split(h, "$"); len(parts) != 4 {
		t.Errorf("hash %q should have 4 $-separated parts", h)
	}
}
