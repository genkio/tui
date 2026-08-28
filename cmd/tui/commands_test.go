package main

import (
	"strings"
	"testing"
)

func TestRemovedWebFlagIsRejected(t *testing.T) {
	err := runCommand([]string{"--web"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("old --web flag error = %v", err)
	}
}

func TestCommandsRejectUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{{"all", "extra"}, {"web", "extra"}, {"serve", "extra"}} {
		if err := runCommand(args); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
			t.Errorf("runCommand(%q) error = %v", args, err)
		}
	}
}
