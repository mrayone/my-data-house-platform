package logging

import "testing"

func TestLevelFromEnv(t *testing.T) {
	cases := map[string]string{
		"debug": "DEBUG",
		"DEBUG": "DEBUG",
		"warn":  "WARN",
		"error": "ERROR",
		"info":  "INFO",
		"":      "INFO",
		"bogus": "INFO",
	}
	for in, want := range cases {
		got := levelFromEnv(in).String()
		if got != want {
			t.Errorf("levelFromEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewReturnsLogger(t *testing.T) {
	if l := New(); l == nil {
		t.Fatal("New() retornou nil")
	}
}
