package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"syscall"
)

// listener is Admiral's one TCP listener, which follows the Tailscale mode unless --listen fixed the address.
type listener struct {
	srv      *http.Server
	explicit bool
	fatal    chan error

	mu   sync.Mutex
	ln   net.Listener
	addr string
	err  error // why the last move failed, nil after a good one
	// attempted is the address the last move aimed at; it differs from addr after a failed bind.
	attempted string
	retired   map[net.Listener]bool
	conns     map[net.Conn]struct{}
}

// newListener is given its server later, once the routes that read it exist.
func newListener(ln net.Listener, addr string, explicit bool) *listener {
	return &listener{ln: ln, addr: addr, attempted: addr, explicit: explicit, fatal: make(chan error, 1),
		retired: map[net.Listener]bool{}, conns: map[net.Conn]struct{}{}}
}

func (l *listener) serveWith(srv *http.Server) {
	l.srv = srv
	srv.ConnState = l.track
}

func (l *listener) track(c net.Conn, s http.ConnState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch s {
	case http.StateNew:
		l.conns[c] = struct{}{}
	case http.StateClosed, http.StateHijacked:
		delete(l.conns, c)
	}
}

// start serves the current listener; a listener that fails without having been retired is fatal.
func (l *listener) start() {
	l.mu.Lock()
	ln := l.ln
	l.mu.Unlock()
	go func() {
		err := l.srv.Serve(ln)
		l.mu.Lock()
		gone := l.retired[ln]
		l.mu.Unlock()
		if !gone {
			select {
			case l.fatal <- err:
			default:
			}
		}
	}()
}

func (l *listener) Addr() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.addr
}

// Err is why the last move failed, nil when it didn't.
func (l *listener) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Attempted is the address the last move aimed at, which is the bound one unless that bind failed.
func (l *listener) Attempted() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attempted
}

// Settle records that the address now wanted is the one already bound, which ends a failed move's error.
func (l *listener) Settle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempted, l.err = l.addr, nil
}

// Move binds want and retires the old listener. A bind that fails keeps (or restores) the old one.
// Connections already accepted are left alone; see DropOffLoopback.
func (l *listener) Move(want string) {
	l.mu.Lock()
	old, oldAddr := l.ln, l.addr
	l.attempted = want
	l.mu.Unlock()
	nl, err := net.Listen("tcp", want)
	if err == nil {
		l.swap(nl, want, old)
		return
	}
	// A wider address can overlap the old one on some systems, so only then retry with the old one closed.
	if errors.Is(err, syscall.EADDRINUSE) {
		l.retire(old)
		if nl, err = net.Listen("tcp", want); err == nil {
			l.swap(nl, want, nil)
			return
		}
	}
	log.Printf("admiral: could not listen on %s, keeping %s: %v", want, oldAddr, err)
	l.mu.Lock()
	l.err = fmt.Errorf("could not listen on %s: %w", want, err)
	retired := l.retired[old]
	l.mu.Unlock()
	if !retired {
		return
	}
	restored, rerr := net.Listen("tcp", oldAddr)
	if rerr != nil {
		select {
		case l.fatal <- fmt.Errorf("could not listen on %s again: %w", oldAddr, rerr):
		default:
		}
		return
	}
	l.mu.Lock()
	l.ln = restored
	l.mu.Unlock()
	l.start()
}

func (l *listener) retire(ln net.Listener) {
	l.mu.Lock()
	l.retired[ln] = true
	l.mu.Unlock()
	_ = ln.Close()
}

func (l *listener) swap(nl net.Listener, addr string, old net.Listener) {
	l.mu.Lock()
	l.ln, l.addr, l.attempted, l.err = nl, addr, addr, nil
	l.mu.Unlock()
	if old != nil {
		l.retire(old)
	}
	l.start()
}

// DropOffLoopback closes the connections that came in on an address other than loopback, which the listener no longer serves.
func (l *listener) DropOffLoopback() {
	l.mu.Lock()
	var drop []net.Conn
	for c := range l.conns {
		if a, ok := c.LocalAddr().(*net.TCPAddr); ok && !a.IP.IsLoopback() {
			drop = append(drop, c)
		}
	}
	l.mu.Unlock()
	for _, c := range drop {
		_ = c.Close()
	}
}
