// Command admiral runs the Admiral server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/state"
	"github.com/jbuffin/admiral/web"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "admiral:", err)
		os.Exit(1)
	}
}

// run dispatches on the first argument; subcommands slot in before the
// default, which starts the server.
func run(args []string) error {
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		return fmt.Errorf("unknown command %q", args[0])
	}
	return serve(args)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("admiral", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:7420", "address to listen on, host:port")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir, err := home.Resolve()
	if err != nil {
		return err
	}
	token, err := auth.LoadOrCreateToken(dir)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer cfg.Close()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	// Without tmux Admiral still serves its config; there are no ships and a
	// launch is refused.
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		fmt.Fprintln(os.Stderr, "admiral: tmux not found on PATH: ships are unavailable")
		tmuxPath = ""
	} else if tmuxPath, err = filepath.Abs(tmuxPath); err != nil {
		return err
	}
	if err := launcher.WriteBundle(dir); err != nil {
		return fmt.Errorf("hook bundle: %w", err)
	}
	st, err := state.Open(dir)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	sock := filepath.Join(dir, "tmux.sock")
	hub := server.NewHub()
	host := sessionhost.New(tmuxPath, sock)
	fleet := &ships.Service{Host: host, State: st, Hub: hub, Launcher: &launcher.Launcher{
		Tmux: tmuxPath, Socket: sock, Home: dir, Config: cfg.Current}}

	s := &server.Server{Version: version, UI: web.UI(), Messages: map[string]server.MessageHandler{}, Config: cfg,
		Hub: hub, Modules: []server.Module{fleet}}
	bg, stopBackground := context.WithCancel(context.Background())
	var background sync.WaitGroup
	runs := []func(context.Context){fleet.Run, s.WatchConfig}
	if tmuxPath != "" {
		runs = append(runs, host.Run)
	}
	for _, run := range runs {
		background.Add(1)
		go func() { defer background.Done(); run(bg) }()
	}
	defer func() {
		stopBackground()
		background.Wait()
		_ = st.Flush()
	}()
	srv := &http.Server{Handler: auth.New(token, port).Middleware(s.Handler())}

	fmt.Printf("Admiral is running at http://127.0.0.1:%s/\n", port)
	bindHost, _, _ := net.SplitHostPort(*listen)
	ip, hasIP := server.LANIPv4()
	switch {
	case bindHost != "" && bindHost != "0.0.0.0" && bindHost != "::":
		fmt.Printf("On other devices: not reachable, Admiral listens only on %s (http://127.0.0.1:%s/)\n", bindHost, port)
	case hasIP:
		fmt.Printf("On other devices: http://%s:%s/?token=%s\n", ip, port, token)
	default:
		fmt.Printf("On other devices: no network address found; this Mac is reachable only at http://127.0.0.1:%s/\n", port)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if srv.Shutdown(shutdown) != nil {
			_ = srv.Close()
		}
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		<-drained
		return nil
	}
	return err
}
