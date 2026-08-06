package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseBizConfigKind(t *testing.T) {
	k, err := parseBizConfigKind("client")
	if err != nil || k != bizKindClient {
		t.Fatalf("client: %v %v", k, err)
	}
	k, err = parseBizConfigKind("SERVER")
	if err != nil || k != bizKindServer {
		t.Fatalf("server: %v %v", k, err)
	}
	if _, err := parseBizConfigKind("other"); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateYAMLSyntax(t *testing.T) {
	if err := validateYAMLSyntax([]byte("a: 1\nb:\n  c: true\n")); err != nil {
		t.Fatal(err)
	}
	if err := validateYAMLSyntax([]byte("a: [\n")); err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestResolveBizYAMLPathAbs(t *testing.T) {
	cfg := &AgentConfig{}
	applyAgentDefaults(cfg)
	if runtime.GOOS == "windows" {
		cfg.BizConfig.ClientPath = `C:\tmp\client.yaml`
	} else {
		cfg.BizConfig.ClientPath = "/tmp/client.yaml"
	}
	p, err := resolveBizYAMLPath("/unused/agent.yaml", cfg, bizKindClient)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(cfg.BizConfig.ClientPath)
	if p != want {
		t.Fatalf("got %q want %q", p, want)
	}
}

func TestBizProcessID(t *testing.T) {
	cfg := &AgentConfig{}
	cfg.BizConfig.ClientProcessID = "1"
	cfg.BizConfig.ServerProcessID = "2"
	if bizProcessID(cfg, bizKindClient) != "1" {
		t.Fatal("client id")
	}
	if bizProcessID(cfg, bizKindServer) != "2" {
		t.Fatal("server id")
	}
}
