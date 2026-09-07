package configyaml

import (
	"reflect"
	"testing"
)

func TestProxyGroupOrder(t *testing.T) {
	raw := "mixed-port: 7890\nproxy-groups:\n  - name: 节点选择\n    type: select\n  - { name: \"香港, 自动\", type: url-test }\n  - name: 'Work''s Proxy'\nrules:\n  - MATCH,节点选择\n"
	want := []string{"节点选择", "香港, 自动", "Work's Proxy"}
	if got := ProxyGroupOrder(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestInlineProxyGroupsFallBackToAPIOrder(t *testing.T) {
	if got := ProxyGroupOrder("proxy-groups: [{ name: A }]"); len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}
