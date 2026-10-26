package cli

import (
	"strings"
	"testing"
)

func TestReadLineDoesNotReadAhead(t *testing.T) {
	in := strings.NewReader("one\ntwo\n")
	first, err := readLine(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readLine(in)
	if err != nil {
		t.Fatal(err)
	}
	if first != "one" || second != "two" {
		t.Fatalf("got %q and %q", first, second)
	}
}
