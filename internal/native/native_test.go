package native

import "testing"

func TestNamesAndIsNative(t *testing.T) {
	for _, name := range []string{"sqlite", "github", "linear"} {
		if !IsNative(name) {
			t.Fatalf("%s should be native", name)
		}
	}
	if IsNative("jira") {
		t.Fatal("jira should not be native")
	}
	names := Names()
	names[0] = "changed"
	if Names()[0] != "sqlite" {
		t.Fatal("Names must return a copy")
	}
}
