package main

import (
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
