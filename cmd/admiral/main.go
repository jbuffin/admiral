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
	"os/signal"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/server"
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
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	s := &server.Server{Version: version, UI: web.UI(), Messages: map[string]server.MessageHandler{}}
	srv := &http.Server{Handler: auth.New(token, port).Middleware(s.Handler())}

	fmt.Printf("Admiral is running at http://127.0.0.1:%s/\n", port)
	if ip, ok := server.LANIPv4(); ok {
		fmt.Printf("On other devices: http://%s:%s/?token=%s\n", ip, port, token)
	} else {
		fmt.Printf("On other devices: no network address found; this Mac is reachable only at http://127.0.0.1:%s/\n", port)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if srv.Shutdown(shutdown) != nil {
			_ = srv.Close()
		}
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
