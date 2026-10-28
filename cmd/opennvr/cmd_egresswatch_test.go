package main

import "testing"

func TestEgressWatchCommandIsRegistered(t *testing.T) {
	if commands["egress-watch"] == nil {
		t.Fatal("falta el subcomando egress-watch")
	}
}
