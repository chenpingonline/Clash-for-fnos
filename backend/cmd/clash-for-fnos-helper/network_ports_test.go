package main

import (
	"net"
	"strings"
	"testing"
)

func TestNetworkPortsRejectDuplicate(t *testing.T) {
	raw := "external-controller: 127.0.0.1:9090\nmixed-port: 9090\n"
	if err := validateNetworkPorts(raw, raw, false); err == nil || !strings.Contains(err.Error(), "Controller API") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}
func TestNetworkPortsProbeChangedButNotUnchanged(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	raw := "external-controller: " + l.Addr().String() + "\n"
	if err := validateNetworkPorts(raw, raw, false); err != nil {
		t.Fatal(err)
	}
	if err := validateNetworkPorts(raw, raw, true); err == nil {
		t.Fatal("offline occupied port accepted")
	}
	if err := validateNetworkPorts(raw, "external-controller: 127.0.0.1:1\n", false); err == nil {
		t.Fatal("changed occupied port accepted")
	}
}
func TestNetworkPortsCheckUDP(t *testing.T) {
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.LocalAddr().String())
	old := "external-controller: 127.0.0.1:9090\n"
	err = validateNetworkPorts(old+"mixed-port: "+port+"\n", old, false)
	if err == nil || !strings.Contains(err.Error(), "udp") {
		t.Fatalf("expected UDP conflict: %v", err)
	}
}
