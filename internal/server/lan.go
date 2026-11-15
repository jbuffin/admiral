package server

import (
	"bytes"
	"io"
	"net"
	"os"
)

func bytesReader(b []byte) io.ReadSeeker { return bytes.NewReader(b) }

func LANIPv4() (string, bool) {
	// ADMIRAL_LAN_IP pins the address when the route picks the wrong network, and lets tests run with none.
	if v := os.Getenv("ADMIRAL_LAN_IP"); v != "" {
		if ip := net.ParseIP(v); ip != nil && usable(ip) {
			return ip.To4().String(), true
		}
	}
	// A UDP "connection" only selects a route; no packet is sent.
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		defer c.Close()
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok && usable(a.IP) {
			return a.IP.String(), true
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && usable(n.IP) {
			return n.IP.String(), true
		}
	}
	return "", false
}

func usable(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && !v4.IsLoopback() && !v4.IsLinkLocalUnicast() && !v4.IsUnspecified()
}
