package version

import "testing"

func TestStringIncludesVersionAndCommit(t *testing.T) {
	Version, Commit = "1.2.3", "abc1234"
	if got := String(); got != "1.2.3 (abc1234)" {
		t.Fatalf("got %q", got)
	}
	Commit = ""
	if got := String(); got != "1.2.3" {
		t.Fatalf("got %q", got)
	}
}
