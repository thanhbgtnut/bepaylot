package config

import "testing"

func TestNormalizeContextRoot(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "bepaylot": "/bepaylot", "/bepaylot/": "/bepaylot", " /a/b/ ": "/a/b"} {
		if got := NormalizeContextRoot(in); got != want {
			t.Errorf("NormalizeContextRoot(%q) = %q, want %q", in, got, want)
		}
	}
}
