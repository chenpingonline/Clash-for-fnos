package main

import (
	"fmt"
	"net"
	"strconv"
)

type networkListener struct{ network, address, label string }

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
	old := map[networkListener]bool{}
	if !offline {
		for _, item := range networkListeners(previous) {
			old[item] = true
		}
	}
	for _, item := range next {
		// An unchanged live listener is already owned by the current Core.
		if old[item] {
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
