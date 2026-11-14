package peerproc

import "testing"

func TestParentReadsPastACommandNameWithSpacesAndParentheses(t *testing.T) {
	got, err := statParent("4242 (node (x) y) S 917 4242 4242 0 -1 4194560")
	if err != nil || got != 917 {
		t.Fatalf("parent = %d, %v", got, err)
	}
}
