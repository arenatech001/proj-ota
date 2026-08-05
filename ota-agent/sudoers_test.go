package main

import (
	"strings"
	"testing"
)

func TestValidSudoersUser(t *testing.T) {
	ok := []string{"arenatech", "ota", "user_1", "a"}
	bad := []string{"", "root!", "../x", "a b", "-bad", strings.Repeat("x", 33)}
	for _, u := range ok {
		if !validSudoersUser(u) {
			t.Errorf("expected valid: %q", u)
		}
	}
	for _, u := range bad {
		if validSudoersUser(u) {
			t.Errorf("expected invalid: %q", u)
		}
	}
}

func TestBuildAgentSudoersBody(t *testing.T) {
	body := buildAgentSudoersBody("arenatech",
		[]string{"/home/arenatech/agent/tools/init-eth0.sh", "/home/arenatech/agent/tools/wifi-watchdog.sh"},
		"/usr/bin/hostnamectl",
		"/usr/bin/tee",
	)
	wantParts := []string{
		"Defaults:arenatech !requiretty",
		"arenatech ALL=(root) NOPASSWD:",
		"/home/arenatech/agent/tools/init-eth0.sh",
		"/home/arenatech/agent/tools/wifi-watchdog.sh",
		"/usr/bin/hostnamectl set-hostname *",
		"/usr/bin/tee /boot/firmware/user-data",
	}
	for _, p := range wantParts {
		if !strings.Contains(body, p) {
			t.Fatalf("missing %q in:\n%s", p, body)
		}
	}
}
