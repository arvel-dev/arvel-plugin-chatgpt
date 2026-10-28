// SPDX-License-Identifier: Apache-2.0
package sanitize

import (
	"strings"
	"testing"
)

func TestCleaner_StreamingSecretsAtEveryBoundary(t *testing.T) {
	for _, secret := range []string{"abc", "long-sensitive-secret", "пароль", "a"} {
		text := "before " + secret + " after"
		for split := 0; split <= len(text); split++ {
			c := New([]string{secret})
			first, tail := c.Split(text[:split], false)
			last, _ := c.Split(tail+text[split:], true)
			if first+last != c.String(text) {
				t.Fatalf("secret length %d split %d: %q", len(secret), split, first+last)
			}
		}
	}
}

func TestCleaner_OverlapsAndBytewiseUTF8(t *testing.T) {
	c := New([]string{"abcd", "bcd", "cd", "secret"})
	input := "Ж abcd and secret end"
	var output strings.Builder
	tail := ""
	for i := range len(input) {
		text, left := c.Split(tail+input[i:i+1], false)
		output.WriteString(text)
		tail = left
		if len(tail) > 2*c.longest {
			t.Fatal("unbounded suffix")
		}
	}
	text, _ := c.Split(tail, true)
	output.WriteString(text)
	if output.String() != c.String(input) {
		t.Fatalf("%q", output.String())
	}
}
