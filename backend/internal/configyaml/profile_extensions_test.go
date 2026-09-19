package configyaml

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestApplyProfileExtensions(t *testing.T) {
	raw := []byte("proxies:\n  - {name: old, type: ss}\nproxy-groups:\n  - name: select\n    type: select\n    proxies: [old]\nrules:\n  - MATCH,DIRECT\ndns:\n  enable: false\n")
	extensions := map[string]string{
		"rules":    "prepend: [\"DOMAIN-SUFFIX,example.com,select\"]\nappend: [\"MATCH,select\"]\ndelete: [\"MATCH,DIRECT\"]\n",
		"proxies":  "prepend:\n  - {name: added, type: socks5, server: 127.0.0.1, port: 1080}\nappend: []\ndelete: [old]\n",
		"groups":   "prepend: []\nappend: []\ndelete: []\n",
		"override": "dns:\n  enable: true\n  ipv6: false\n",
		"script":   "function main(config, profileName) { config.profileName = profileName; return config; }",
	}
	out, err := ApplyProfileExtensions(raw, "测试订阅", extensions)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["profileName"] != "测试订阅" {
		t.Fatalf("profileName=%#v", got["profileName"])
	}
	rules := got["rules"].([]any)
	if len(rules) != 2 || rules[1] != "MATCH,select" {
		t.Fatalf("rules=%#v", rules)
	}
	proxies := got["proxies"].([]any)
	if len(proxies) != 1 || sequenceIdentity(proxies[0]) != "added" {
		t.Fatalf("proxies=%#v", proxies)
	}
	group := got["proxy-groups"].([]any)[0].(map[string]any)
	members := group["proxies"].([]any)
	if len(members) != 1 || members[0] != "added" {
		t.Fatalf("members=%#v", members)
	}
	dns := got["dns"].(map[string]any)
	if dns["enable"] != true || dns["ipv6"] != false {
		t.Fatalf("dns=%#v", dns)
	}
}

func TestValidateExtensionRejectsInvalidContent(t *testing.T) {
	if err := ValidateExtension("rules", "prepend: ["); err == nil {
		t.Fatal("expected YAML error")
	}
	if err := ValidateExtension("script", "function main("); err == nil {
		t.Fatal("expected script syntax error")
	}
}

func TestScriptCanAppendProxyLikeClashVerge(t *testing.T) {
	raw := []byte("proxies: []\nproxy-groups:\n  - name: 手动选择\n    type: select\n    proxies: [DIRECT]\n")
	script := `function main(config) {
  const node = { name: "脚本节点", type: "socks5", server: "127.0.0.1", port: 1080 };
  const proxies = config["proxies"] || [];
  proxies.push(node);
  config["proxies"] = proxies;
  const groups = config["proxy-groups"] || [];
  groups[0].proxies.push(node.name);
  console.log("added", node.name);
  return config;
}`
	out, err := ApplyProfileExtensions(raw, "测试订阅", map[string]string{"script": script})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	proxies, _ := got["proxies"].([]any)
	if len(proxies) != 1 || sequenceIdentity(proxies[0]) != "脚本节点" {
		t.Fatalf("proxies=%#v", proxies)
	}
	groups, _ := got["proxy-groups"].([]any)
	members, _ := groups[0].(map[string]any)["proxies"].([]any)
	if len(members) != 2 || members[1] != "脚本节点" {
		t.Fatalf("members=%#v", members)
	}
}

func TestProfileExtensionChainMatchesClashVergeOrder(t *testing.T) {
	raw := []byte("marker: raw\nproxies: []\nproxy-groups:\n  - name: select\n    type: select\n    proxies: [DIRECT]\nsettings:\n  base: true\n")
	globalExtensions := map[string]string{
		"override": "marker: global-override\norder: [global-override]\nvalues: [global]\nsettings:\n  global: true\n",
		"script": `function main(config) {
  if (config.marker !== "global-override") throw new Error("global override was not applied first");
  if (config.proxies.length !== 1) throw new Error("profile sequence editors were not applied first");
  config.marker = "global-script";
  config.order.push("global-script");
  config.globalScriptApplied = true;
  return config;
}`,
	}
	profileExtensions := map[string]string{
		"proxies":  "prepend:\n  - {name: sequence-node, type: socks5, server: 127.0.0.1, port: 1080}\nappend: []\ndelete: []\n",
		"override": "marker: profile-override\nvalues: [profile]\nsettings:\n  profile: true\n",
		"script": `function main(config, profileName) {
  if (config.marker !== "profile-override") throw new Error("profile override was not applied before profile script");
  if (!config.globalScriptApplied) throw new Error("global script was not applied before profile extensions");
  config.marker = "profile-script";
  config.profileName = profileName;
  config.order.push("profile-script");
  return config;
}`,
	}
	out, err := ApplyProfileExtensionChain(raw, "测试订阅", globalExtensions, profileExtensions)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["marker"] != "profile-script" || got["profileName"] != "测试订阅" {
		t.Fatalf("marker=%#v profileName=%#v", got["marker"], got["profileName"])
	}
	order, _ := got["order"].([]any)
	wantOrder := []string{"global-override", "global-script", "profile-script"}
	if len(order) != len(wantOrder) {
		t.Fatalf("order=%#v", order)
	}
	for index, want := range wantOrder {
		if order[index] != want {
			t.Fatalf("order=%#v", order)
		}
	}
	values, _ := got["values"].([]any)
	if len(values) != 1 || values[0] != "profile" {
		t.Fatalf("values=%#v", values)
	}
	settings, _ := got["settings"].(map[string]any)
	if settings["base"] != true || settings["global"] != true || settings["profile"] != true {
		t.Fatalf("settings=%#v", settings)
	}
}
