package configyaml

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestUserOverridesOnlyReplaceExplicitSettings(t *testing.T) {
	raw := []byte("# subscription\nipv6: true\nunified-delay: false\ngeo-auto-update: false\ntun: {enable: true, mtu: 1400, stack: system}\nproxies: []\nrules:\n  - MATCH,DIRECT\n")
	out, err := MergeOverrides(raw, map[string]any{"ipv6": false, "geo-auto-update": true, "tun": map[string]any{"enable": false, "route-exclude-address": []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	tun := got["tun"].(map[string]any)
	if got["ipv6"] != false || got["unified-delay"] != false || got["geo-auto-update"] != true || tun["enable"] != false || tun["mtu"] != 1400 || tun["stack"] != "system" {
		t.Fatalf("%s", out)
	}
	if !strings.Contains(string(out), "# subscription") || len(got["rules"].([]any)) != 1 {
		t.Fatal("lost subscription content")
	}
}
func TestNoOverridesPreservesOriginalBytes(t *testing.T) {
	raw := []byte("tun: { enable: true }\n")
	out, err := MergeOverrides(raw, nil)
	if err != nil || string(out) != string(raw) {
		t.Fatalf("%s %v", out, err)
	}
}

func TestOverridePreservesReferencedAnchor(t *testing.T) {
	out, err := MergeOverrides([]byte("tun: &tun {enable: true, mtu: 1400}\nother: *tun\n"), map[string]any{"tun": map[string]any{"enable": false}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if got["tun"].(map[string]any)["mtu"] != 1400 {
		t.Fatal("lost untouched field")
	}
}
