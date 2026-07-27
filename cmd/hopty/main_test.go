package main

import "testing"

func TestVersion(t *testing.T) {
	if err := run([]string{"--version"}); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsCommand(t *testing.T) {
	if err := run([]string{"pair"}); err == nil {
		t.Fatal("run accepted an unavailable command")
	}
}
