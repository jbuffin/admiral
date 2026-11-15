//go:build e2e

package main

import (
	"errors"
	"net"
	"os"
	"sync"

	"github.com/jbuffin/admiral/internal/server"
)

// listenTCP is the test build's way onto a LAN with no network: with ADMIRAL_E2E_LAN_PORT set, a wildcard bind
// also opens 127.0.0.1 on that port, and its connections look like a LAN peer reaching ADMIRAL_LAN_IP.
// It closes when Admiral moves to loopback, as a real LAN address stops answering. A release is built without this file.
func listenTCP(addr string) (net.Listener, error) {
	wild, err := net.Listen("tcp", addr)
	lanPort := os.Getenv("ADMIRAL_E2E_LAN_PORT")
	host, _, _ := net.SplitHostPort(addr)
	if err != nil || lanPort == "" || (host != "" && !net.ParseIP(host).IsUnspecified()) {
		return wild, err
	}
	lan, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", lanPort))
	if err != nil {
		wild.Close()
		return nil, err
	}
	ip, _ := server.LANIPv4()
	l := &lanListener{Listener: wild, conns: make(chan accepted), done: make(chan struct{}),
		local: &net.TCPAddr{IP: net.ParseIP(ip), Port: wild.Addr().(*net.TCPAddr).Port}}
	go l.serve(wild, false)
	go l.serve(lan, true)
	l.lan = lan
	return l, nil
}

type accepted struct {
	c   net.Conn
	err error
}

type lanListener struct {
	net.Listener
	lan   net.Listener
	local *net.TCPAddr
	conns chan accepted
	done  chan struct{}
	once  sync.Once
}

func (l *lanListener) serve(ln net.Listener, fromLAN bool) {
	for {
		c, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err == nil && fromLAN {
			peer := c.RemoteAddr().(*net.TCPAddr)
			c = lanConn{Conn: c, local: l.local, remote: &net.TCPAddr{IP: net.IPv4(192, 0, 2, 20), Port: peer.Port}}
		}
		select {
		case l.conns <- accepted{c, err}:
		case <-l.done:
			if c != nil {
				c.Close()
			}
			return
		}
	}
}

func (l *lanListener) Accept() (net.Conn, error) {
	select {
	case a := <-l.conns:
		return a.c, a.err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *lanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return errors.Join(l.Listener.Close(), l.lan.Close())
}

type lanConn struct {
	net.Conn
	local, remote net.Addr
}

func (c lanConn) LocalAddr() net.Addr  { return c.local }
func (c lanConn) RemoteAddr() net.Addr { return c.remote }
