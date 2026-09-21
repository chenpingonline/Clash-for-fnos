package main

import (
	"fmt"
	"net"
	"strconv"
)

type networkListener struct{ network, address, label string }

func networkListenersOverlap(left, right networkListener) bool {
	if left.network != right.network {
		return false
	}
	leftHost, leftPort, leftErr := net.SplitHostPort(left.address)
	rightHost, rightPort, rightErr := net.SplitHostPort(right.address)
	if leftErr != nil || rightErr != nil || leftPort != rightPort {
		return false
	}
	leftIP, rightIP := net.ParseIP(leftHost), net.ParseIP(rightHost)
	if leftIP == nil || rightIP == nil {
		return leftHost == rightHost
	}
	leftIPv4, rightIPv4 := leftIP.To4() != nil, rightIP.To4() != nil
	if leftIPv4 != rightIPv4 {
		return false
	}
	return leftIP.Equal(rightIP) || leftIP.IsUnspecified() || rightIP.IsUnspecified()
}

func networkListeners(raw string) []networkListener {
	controller, _, _ := parseController(raw)
	result := []networkListener{{"tcp", controller, "Controller API"}}
	host := "127.0.0.1"
	if yamlBoolean(raw, "allow-lan", false) {
		host = "0.0.0.0"
		if bind, ok := yamlScalarValue(raw, "bind-address"); ok && bind != "" && bind != "*" {
			host = bind
		}
	}
	for _, item := range []struct {
		key, label string
		udp        bool
	}{
		{"mixed-port", "混合代理端口", true}, {"port", "HTTP(S) 代理端口", false}, {"socks-port", "SOCKS5 代理端口", true}, {"redir-port", "Redir 透明代理端口", false}, {"tproxy-port", "TProxy 透明代理端口", true},
	} {
		value, _ := yamlScalarValue(raw, item.key)
		port, _ := strconv.Atoi(value)
		if port <= 0 {
			continue
		}
		address := net.JoinHostPort(host, strconv.Itoa(port))
		result = append(result, networkListener{"tcp", address, item.label})
		if item.udp {
			result = append(result, networkListener{"udp", address, item.label})
		}
	}
	return result
}
func validateNetworkPorts(raw, previous string, offline bool) error {
	next := networkListeners(raw)
	// Reject duplicate TCP/UDP ports before probing, including currently owned ports.
	used := map[string]string{}
	for _, item := range next {
		_, port, err := net.SplitHostPort(item.address)
		if err != nil {
			return fmt.Errorf("%s 监听地址无效: %w", item.label, err)
		}
		key := item.network + ":" + port
		if label, ok := used[key]; ok {
			return fmt.Errorf("端口冲突：%s 已用于 %s，%s 请更换端口", port, label, item.label)
		}
		used[key] = item.label
	}
	old := []networkListener{}
	if !offline {
		old = networkListeners(previous)
	}
	for _, item := range next {
		// The running Core releases its listeners while applying the candidate.
		// Treat a wildcard/specific-address transition on the same port as the
		// current Core's listener instead of probing it against itself.
		ownedByCurrentCore := false
		for _, current := range old {
			if networkListenersOverlap(current, item) {
				ownedByCurrentCore = true
				break
			}
		}
		if ownedByCurrentCore {
			continue
		}
		var err error
		if item.network == "udp" {
			var conn net.PacketConn
			conn, err = net.ListenPacket(item.network, item.address)
			if err == nil {
				conn.Close()
			}
		} else {
			var conn net.Listener
			conn, err = net.Listen(item.network, item.address)
			if err == nil {
				conn.Close()
			}
		}
		if err != nil {
			return fmt.Errorf("%s 无法使用 %s (%s)，请更换端口或释放占用后重试: %w", item.label, item.address, item.network, err)
		}
	}
	return nil
}
