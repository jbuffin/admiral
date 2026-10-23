// Command admiral runs the Admiral server.
package main

import (
	"context"
	"encoding/json"
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
	"github.com/jbuffin/admiral/internal/autopilot"
	"github.com/jbuffin/admiral/internal/bridge"
	"github.com/jbuffin/admiral/internal/cli"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/launchorders"
	"github.com/jbuffin/admiral/internal/lockroute"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/resources"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
	"github.com/jbuffin/admiral/internal/state"
	"github.com/jbuffin/admiral/web"
)

// version and commit are stamped by make release and make build (-ldflags -X).
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var help *cli.HelpError
		if errors.As(err, &help) {
			fmt.Println(help)
			return
		}
		var exit *cli.ExitError
		if errors.As(err, &exit) {
			if exit.Msg != "" {
				fmt.Fprintln(os.Stderr, "admiral:", exit.Msg)
			}
			os.Exit(exit.Code)
		}
		fmt.Fprintln(os.Stderr, "admiral:", err)
		var usage *cli.UsageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// run dispatches on the first argument; subcommands slot in before the
// default, which starts the server.
func run(args []string) error {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Printf("admiral %s (%s)\n", version, commit)
		return nil
	}
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return serve(args)
	}
	std := cli.Std()
	for _, a := range args[1:] {
		if a == "--" {
			break // the rest is a command's own arguments
		}
		if cli.IsHelp(a) {
			fmt.Println(cli.Usage(args[0]))
			return nil
		}
	}
	switch args[0] {
	case "setup":
		return cli.Setup(args[1:], std)
	case "fleet":
		return cli.Fleet(args[1:], std)
	case "config":
		return cli.Config(args[1:], std)
	case "token":
		return cli.Token(args[1:], std)
	case "ships":
		return cli.Ships(args[1:], std)
	case "attach":
		return cli.Attach(args[1:], std)
	case "completion":
		return cli.Completion(args[1:], std)
	case "service":
		return cli.Service(args[1:], std)
	case "hook":
		return cli.Hook(args[1:], std)
	case "outcomes":
		return cli.Outcomes(args[1:], std)
	case "lock":
		return cli.Lock(args[1:], std)
	}
	return &cli.UsageError{Msg: fmt.Sprintf("unknown command %q", args[0])}
}

// seedSources gives a first run its "Orders" source. It looks only at startup,
// so a sources key deleted by hand stays gone until the next start, and it
// adds nothing to a config that does not validate or already has the key.
func seedSources(cfg *config.Manager) {
	if cfg.View().Error != nil || cfg.Current().Sources != nil {
		return
	}
	err := cfg.SetSources(json.RawMessage(`{"orders":{"plugin":"freeform","name":"Orders"}}`))
	if err != nil {
		fmt.Fprintln(os.Stderr, "admiral: could not add the Orders source to the config:", err)
	}
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
	// No configuration means no fleets (a valid file without any, or none yet).
	if cfg.View().Error == nil && len(cfg.Current().Fleets) == 0 {
		if _, err := cli.OfferSetup(cli.Std()); err != nil {
			return err
		}
	}
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
	if exe, err := os.Executable(); err == nil {
		if err := launcher.LinkBinary(dir, exe); err != nil {
			return fmt.Errorf("hook binary link: %w", err)
		}
	}
	st, err := state.Open(dir)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	sock := filepath.Join(dir, "tmux.sock")
	hub := server.NewHub()
	host := sessionhost.New(tmuxPath, sock)
	outcomeLog := outcomes.New(filepath.Join(dir, "outcomes"), func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "admiral: "+format+"\n", args...)
	})
	shipService := &ships.Service{Host: host, State: st, Hub: hub, Outcomes: outcomeLog, Launcher: &launcher.Launcher{
		Tmux: tmuxPath, Socket: sock, Home: dir, Config: cfg.Current,
		Kept: func(session string) bool { _, ok := st.KeptInbox(session); return ok }}}

	if err := sources.InstallPlugins(dir); err != nil {
		fmt.Fprintln(os.Stderr, "admiral: could not install the bundled plugins:", err)
	}
	seedSources(cfg)
	supervisor := &sources.Supervisor{Home: dir, Config: cfg}
	sourceService := &sources.Service{Home: dir, Config: cfg, Sup: supervisor, Hub: hub, Ships: shipService.List, State: st}
	apStore, err := autopilot.OpenStore(dir)
	if err != nil {
		return fmt.Errorf("autopilot: %w", err)
	}
	launchService := &launchorders.Service{Config: cfg, State: st, Ships: shipService.List, Orders: sourceService}
	// A ship launched but not yet listed by the host counts as running.
	allShips := func() []ships.Ship { return append(shipService.List(), shipService.Pending()...) }
	sourceService.Ships = allShips
	engine := &autopilot.Engine{
		Config: cfg.Current,
		Ships:  allShips,
		Sources: func() []autopilot.Source {
			var out []autopilot.Source
			for _, l := range sourceService.Listings() {
				out = append(out, autopilot.Source{Key: l.Key, Listed: l.Listed, Panels: l.Panels})
			}
			return out
		},
		Paused: st.Paused,
		Dir:    launchService.Dir,
		Launch: shipService.LaunchOrder,
		Record: func(r outcomes.Record) { outcomeLog.Enqueue(r, nil) },
		Now:    time.Now,
		Store:  apStore,
		OnHot:  sourceService.Notify,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "admiral: "+format+"\n", args...)
		},
	}
	sourceService.Hot = engine.Hot
	sourceService.WaitingFor = engine.WaitingFor
	// A check, a lock or a reservation changing frees or fills a unit: autopilot
	// looks again and the UI is told.
	var resourceService *resources.Service
	resourceSet := resources.New(resources.Options{Config: cfg.Current, Ships: allShips,
		OnChange: func() { resourceService.Broadcast(); engine.Poke() },
		// The engine drops its reservations inside a pass; poking it for each would
		// make a refused launch (a 503, a 409) start the next pass at once.
		OnDrop: func() { resourceService.Broadcast() }})
	resourceService = &resources.Service{Set: resourceSet, Hub: hub}
	engine.Resources = resourceSet
	engine.OnWait = sourceService.Notify
	sourceService.OnLaunched = engine.Launched
	supervisor.OnChange = func() { sourceService.Notify(); engine.Poke() }
	stopping := make(chan struct{})
	lockRoute := &lockroute.Route{Set: resourceSet, Stopping: stopping, OnTake: func() { resourceService.Broadcast() }}
	bridgeService := bridge.New(tmuxPath, sock, shipService)
	if exe, err := os.Executable(); err == nil && tmuxPath != "" {
		bridgeService.Host, bridgeService.Setup = host, &bridge.SetupCommand{Exe: exe, Home: dir}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	// A ship that sailed off with an item unanswered is known from its record.
	inboxService := &inbox.Service{Home: absDir, Hub: hub, Outcomes: outcomeLog, Kept: st, Ship: func(session string) (string, string, bool) {
		if sh, ok := shipService.Get(session); ok {
			return sh.Name, sh.Harness, true
		}
		k, ok := st.KeptInbox(session)
		return k.Name, k.Harness, ok
	}, Dock: func(session string) ([]string, string) {
		fleet, harness := "", ""
		if sh, ok := shipService.Get(session); ok {
			fleet, harness = sh.Fleet, sh.Harness
		} else if k, ok := st.KeptInbox(session); ok {
			fleet, harness = k.Fleet, k.Harness
		} else {
			return nil, "ship " + session + " is unknown"
		}
		f, ok := cfg.Current().Fleet(fleet)
		if !ok {
			return nil, "fleet " + fleet + " is not in the config"
		}
		dock, ok := f.Harness(harness)
		if !ok {
			return nil, "fleet " + fleet + " has no " + harness + " dock in the config"
		}
		return dock.Environ(), ""
	}}
	shipService.Orders = sourceService
	shipService.OnChange = func() {
		sourceService.Notify()
		bridgeService.ShipsChanged()
		resourceSet.Wake() // a ship that stopped holding a unit frees it
		resourceService.Broadcast()
		engine.Poke()
		inboxService.Notify()
	}

	s := &server.Server{Version: version, Commit: commit, UI: web.UI(), Messages: map[string]server.MessageHandler{}, Config: cfg,
		Hub: hub, Modules: []server.Module{shipService, sourceService, inboxService,
			&launchorders.Service{Config: cfg, State: st, Ships: shipService.List, Orders: sourceService}, bridgeService, resourceService, lockRoute}}
	bg, stopBackground := context.WithCancel(context.Background())
	var background sync.WaitGroup
	supervisor.Start(bg)
	// The ships' run stops first and the log closes next, while the host is
	// still up, so a written `ended` can still set its marker.
	shipsCtx, stopShips := context.WithCancel(bg)
	shipsDone := make(chan struct{})
	go func() { defer close(shipsDone); shipService.Run(shipsCtx) }()
	// Autopilot stops with the ships' run, before the log closes, so a record
	// it writes at shutdown is kept.
	engineDone := make(chan struct{})
	go func() { defer close(engineDone); engine.Run(shipsCtx) }()
	configChanges, unsubscribe := cfg.Subscribe()
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-configChanges:
				sourceService.Notify() // an autopilot-only edit does not restart a source
				resourceSet.Refresh()  // the resources' checks run at once
				resourceSet.Wake()
				resourceService.Broadcast()
				engine.Poke()
			case <-shipsCtx.Done():
				return
			}
		}
	}()
	runs := []func(context.Context){s.WatchConfig, sourceService.Run, bridgeService.Run, resourceSet.Run, inboxService.Run}
	if tmuxPath != "" {
		runs = append(runs, host.Run)
	}
	for _, run := range runs {
		background.Add(1)
		go func() { defer background.Done(); run(bg) }()
	}
	defer func() {
		stopShips()
		<-shipsDone
		<-engineDone
		outcomeLog.Close()
		stopBackground()
		background.Wait()
		supervisor.Wait()
		_ = st.Flush()
	}()
	guard := auth.New(token, port)
	background.Add(1)
	go func() { defer background.Done(); guard.Watch(bg, dir) }()
	srv := &http.Server{Handler: guard.Middleware(s.Handler())}
	srv.RegisterOnShutdown(func() { close(stopping) }) // a held lock would hold up the shutdown

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
