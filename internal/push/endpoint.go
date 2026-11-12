package push

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// CheckEndpoint is the subscribe-time rule: an https URL with a host. Where it may point is decided at dial time.
func CheckEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return errors.New("a push endpoint must be an https URL")
	}
	return nil
}

var (
	cgnat       = netip.MustParsePrefix("100.64.0.0/10")
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	reserved    = netip.MustParsePrefix("240.0.0.0/4")
	nat64       = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local  = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour   = netip.MustParsePrefix("2002::/16")
)

// DialControl refuses loopback, private, link-local, unspecified, multicast, reserved and carrier-grade NAT (tailnet) addresses,
// including an IPv4 one wrapped in a NAT64 or 6to4 IPv6 address, so a subscription cannot aim Admiral's POST at a service on this Mac or the network.
// It runs on the resolved address.
func DialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if blocked(ip.Unmap()) {
		return fmt.Errorf("refusing to push to a local address (%s)", ip)
	}
	return nil
}

func blocked(ip netip.Addr) bool {
	if ip.Is6() {
		b := ip.As16()
		switch {
		case nat64.Contains(ip):
			return blocked(netip.AddrFrom4([4]byte(b[12:16])))
		case nat64Local.Contains(ip):
			return blocked(netip.AddrFrom4([4]byte{b[6], b[7], b[9], b[10]}))
		case sixToFour.Contains(ip):
			return blocked(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		cgnat.Contains(ip) || thisNetwork.Contains(ip) || reserved.Contains(ip)
}

// SafeClient is the sender's HTTP client: no proxy, no redirects, and DialControl on every connection unless allowLocal
// (the e2e seam). A nil roots pool means the system's.
func SafeClient(roots *x509.CertPool, allowLocal bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowLocal {
		d.Control = DialControl
	}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	if roots != nil {
		tr.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
