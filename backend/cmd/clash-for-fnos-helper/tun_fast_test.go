package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceNestedBooleanPreservesTunConfiguration(t *testing.T) {
	original := "mixed-port: 7890\ntun:\n    stack: mixed\n    enable: false # keep comment\n    custom-option: value\ndns:\n  enable: true\n"
	updated, err := replaceNestedBoolean(original, "tun", "enable", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"    enable: true # keep comment", "    stack: mixed", "    custom-option: value", "dns:\n  enable: true"} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("missing %q in:\n%s", expected, updated)
		}
	}
	if strings.Count(updated, "enable: true") != 2 {
		t.Fatalf("updated the wrong YAML section:\n%s", updated)
	}
}

func TestReplaceNestedBooleanAddsMissingTunSection(t *testing.T) {
	updated, err := replaceNestedBoolean("mixed-port: 7890\n", "tun", "enable", true)
	if err != nil {
		t.Fatal(err)
	}
	if updated != "mixed-port: 7890\ntun:\n  enable: true\n" {
		t.Fatalf("unexpected YAML:\n%s", updated)
	}
}

func TestReplaceNestedBooleanRejectsInlineTun(t *testing.T) {
	_, err := replaceNestedBoolean("tun: {enable: false, stack: mixed}\n", "tun", "enable", true)
	if err == nil || !strings.Contains(err.Error(), "行内 YAML") {
		t.Fatalf("expected inline YAML error, got %v", err)
	}
}

func TestTunTransactionCannotActivateBeforeValidation(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate.yaml")
	target := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(candidate, []byte("tun:\n  enable: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHelper(helperConfig{})
	h.transactions["tun"] = &transaction{Target: target, Candidate: candidate, ValidationRequired: true}
	if _, err := h.activateConfig(context.Background(), "tun"); err == nil || !strings.Contains(err.Error(), "尚未通过 Mihomo 校验") {
		t.Fatalf("expected validation guard, got %v", err)
	}
	h.transactions["tun"].Validated = true
	if _, err := h.activateConfig(context.Background(), "tun"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTunTransactionRunsMihomoTest(t *testing.T) {
	root := t.TempDir()
	core := filepath.Join(root, "mihomo")
	candidate := filepath.Join(root, "candidate.yaml")
	target := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(core, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("tun:\n  enable: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHelper(helperConfig{managedCore: core})
	h.transactions["tun"] = &transaction{Target: target, Candidate: candidate, ValidationRequired: true}
	result, err := h.validateConfigTransaction(context.Background(), "tun")
	if err != nil {
		t.Fatal(err)
	}
	if result["method"] != "mihomo-test" || !h.transactions["tun"].Validated {
		t.Fatalf("unexpected validation result: %#v", result)
	}
}
