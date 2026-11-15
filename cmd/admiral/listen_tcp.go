//go:build !e2e

package main

import "net"

func listenTCP(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
