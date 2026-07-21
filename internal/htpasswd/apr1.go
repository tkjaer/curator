// Package htpasswd produces Apache/nginx-compatible apr1 (MD5-crypt) password
// hashes for .htpasswd files. apr1 is understood by both servers on every
// platform, unlike bcrypt whose nginx support is platform-dependent.
package htpasswd

import (
	"crypto/md5"
	"crypto/rand"
	"strings"
)

const (
	magic  = "$apr1$"
	itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// Hash returns an apr1 hash of password using a random salt.
func Hash(password string) string {
	return hashWithSalt(password, randomSalt())
}

func randomSalt() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(itoa64[int(c)%64])
	}
	return sb.String()
}

func hashWithSalt(password, salt string) string {
	pw := []byte(password)
	s := []byte(salt)

	ctx := md5.New()
	ctx.Write(pw)
	ctx.Write([]byte(magic))
	ctx.Write(s)

	altSum := md5.Sum(concat(pw, s, pw))
	for i := len(pw); i > 0; i -= 16 {
		n := i
		if n > 16 {
			n = 16
		}
		ctx.Write(altSum[:n])
	}

	for i := len(pw); i > 0; i >>= 1 {
		if i&1 == 1 {
			ctx.Write([]byte{0})
		} else {
			ctx.Write(pw[:1])
		}
	}
	final := ctx.Sum(nil)

	for i := 0; i < 1000; i++ {
		c := md5.New()
		if i&1 == 1 {
			c.Write(pw)
		} else {
			c.Write(final)
		}
		if i%3 != 0 {
			c.Write(s)
		}
		if i%7 != 0 {
			c.Write(pw)
		}
		if i&1 == 1 {
			c.Write(final)
		} else {
			c.Write(pw)
		}
		final = c.Sum(nil)
	}

	return magic + salt + "$" + encode(final)
}

func encode(final []byte) string {
	var sb strings.Builder
	to64 := func(v uint, n int) {
		for ; n > 0; n-- {
			sb.WriteByte(itoa64[v&0x3f])
			v >>= 6
		}
	}
	to64(uint(final[0])<<16|uint(final[6])<<8|uint(final[12]), 4)
	to64(uint(final[1])<<16|uint(final[7])<<8|uint(final[13]), 4)
	to64(uint(final[2])<<16|uint(final[8])<<8|uint(final[14]), 4)
	to64(uint(final[3])<<16|uint(final[9])<<8|uint(final[15]), 4)
	to64(uint(final[4])<<16|uint(final[10])<<8|uint(final[5]), 4)
	to64(uint(final[11]), 2)
	return sb.String()
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
