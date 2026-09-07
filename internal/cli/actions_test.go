package cli

import "testing"

func TestActionArgumentsNeverOpenSession(t *testing.T) {
	for _, args := range [][]string{
		{"react", "--help"}, {"unsend", "--help"}, {"react", "Alice", "--message", "123"},
		{"react", "Alice", "--message", "123", "--reaction", "bad"},
		{"react", "Alice", "--message", "123", "--reaction", "love", "--remove"},
		{"unsend", "Alice", "--message", "not-id"}, {"unsend"},
		{"send", "Alice", "--text", "reply", "--reply-to", "invalid"},
	} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("opened session for invalid arguments"); return nil, nil }
		_ = a.Run(args)
	}
}
