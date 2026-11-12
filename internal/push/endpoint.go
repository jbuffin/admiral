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

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// DialControl refuses loopback, private, link-local, unspecified, multicast and carrier-grade NAT (tailnet) addresses,
// so a subscription cannot aim Admiral's POST at a service on this Mac or the network. It runs on the resolved address.
func DialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip) {
		return fmt.Errorf("refusing to push to a local address (%s)", ip)
	}
	return nil
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
