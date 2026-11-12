package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/autopilotgate"
	"github.com/jbuffin/admiral/internal/bridge"
	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/cli"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crewapi"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/harnesscaps"
	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/launchorders"
	"github.com/jbuffin/admiral/internal/lockroute"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/probes"
	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/resources"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/shipconv"
	"github.com/jbuffin/admiral/internal/shipdiff"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
	"github.com/jbuffin/admiral/internal/state"
	"github.com/jbuffin/admiral/internal/tailscale"
	"github.com/jbuffin/admiral/internal/usage"
	"github.com/jbuffin/admiral/internal/usageapi"
	"github.com/jbuffin/admiral/internal/uselive"
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
		// Guard has already written its reason to stderr, which is all the agent reads.
		var denied *cli.GuardDenied
		if errors.As(err, &denied) {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "admiral:", err)
		var usage *cli.UsageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

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
	case "ships":
		return cli.Ships(args[1:], std)
	case "attach":
		return cli.Attach(args[1:], std)
	case "crew":
		return cli.Crew(args[1:], std)
	case "completion":
		return cli.Completion(args[1:], std)
	case "service":
		return cli.Service(args[1:], std)
	case "hook":
		return cli.Hook(args[1:], std)
	case "statusline":
		return cli.Statusline(args[1:], std)
	case "guard":
		return cli.Guard(args[1:], std)
	case "advise":
		return cli.Advise(args[1:], std)
	case "outcomes":
		return cli.Outcomes(args[1:], std)
	case "lock":
		return cli.Lock(args[1:], std)
	case "inbox":
		return cli.Inbox(args[1:], std)
	case "orders":
		return cli.Orders(args[1:], std)
	case "pair":
		return cli.Pair(args[1:], std)
	case "devices":
		return cli.Devices(args[1:], std)
	case "chat":
		return cli.Chat(args[1:], std)
	case "codex-pane":
		return cli.CodexPane(args[1:], std)
	}
	return &cli.UsageError{Msg: fmt.Sprintf("unknown command %q", args[0])}
}

// remoteRoutes puts /api/remote on the server's mux.
type remoteRoutes struct{ *tailscale.Route }

func (remoteRoutes) Connected(*server.Client) {}

// pushDevices is the sender's view of the device store.
type pushDevices struct{ guard *auth.Auth }

func (p pushDevices) Subscriptions() []push.Target {
	var out []push.Target
	for _, s := range p.guard.Subscriptions() {
		out = append(out, push.Target{Device: s.Device, Subscription: push.Subscription{Endpoint: s.Endpoint, P256dh: s.P256dh, Auth: s.Auth}})
	}
	return out
}

func (p pushDevices) Drop(device, endpoint string) { p.guard.DropSubscription(device, endpoint) }

// guardRoutes puts the device API on the server's mux.
type guardRoutes struct{ *auth.Auth }

func (guardRoutes) Connected(*server.Client) {}

// pairingBase is read per call: in Tailscale mode it is the ts.net URL when serve has one.
func pairingBase(port string, cfg func() *config.Config) string {
	if cfg().Remote.Tailscale {
		if base := tailscale.Base(context.Background(), port); base != "" {
			return base
		}
	}
	if ip, ok := server.LANIPv4(); ok {
		return "http://" + net.JoinHostPort(ip, port)
	}
	return "http://127.0.0.1:" + port
}

// listenAddr: an explicit --listen always wins; otherwise Tailscale mode keeps Admiral off the LAN,
// and so does a config that couldn't be read, since it may have turned the mode on.
func listenAddr(explicit string, cfg *config.Config, unreadable bool) string {
	port := "7420"
	if p := os.Getenv("ADMIRAL_PORT"); p != "" { // a test and advanced setting; --listen sets the whole address
		if n, err := strconv.Atoi(p); err == nil && n >= 1 && n <= 65535 {
			port = p
		} else {
			log.Printf("admiral: ignoring ADMIRAL_PORT=%q, which is not a port from 1 to 65535", p)
		}
	}
	switch {
	case explicit != "":
		return explicit
	case unreadable || cfg.Remote.Tailscale:
		return "127.0.0.1:" + port
	}
	return "0.0.0.0:" + port
}

// resolveListen offers setup when there are no fleets, then picks the address from the config as setup left it.
func resolveListen(cfg *config.Manager, explicit string, offer func() (bool, error)) (string, error) {
	// No configuration means no fleets (a valid file without any, or none yet).
	if cfg.View().Error == nil && len(cfg.Current().Fleets) == 0 {
		ran, err := offer()
		if err != nil {
			return "", err
		}
		if ran {
			// Setup may have turned Tailscale mode on; the watcher reloads later than listenAddr reads.
			cfg.Reload()
		}
	}
	unreadable := cfg.View().Error != nil
	if unreadable && explicit == "" {
		fmt.Fprintln(os.Stderr, "admiral: config.json couldn't be read, so Admiral listens on loopback only (127.0.0.1)")
	}
	return listenAddr(explicit, cfg.Current(), unreadable), nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("admiral", flag.ContinueOnError)
	listen := fs.String("listen", "", "address to listen on, host:port (default 0.0.0.0:7420, or 127.0.0.1:7420 with Tailscale mode on)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir, err := home.Resolve()
	if err != nil {
		return err
	}
	// Before the first load, so it sees the migrated file.
	migration, err := config.Migrate(dir, version)
	if err != nil {
		return fmt.Errorf("config migration: %w", err)
	}
	if migration != nil {
		// Posted after the rewrite: a crash between the two loses the notice but never
		// duplicates it, and the backup still holds the order texts.
		title, body := migration.Notice()
		if _, err := inbox.WriteNotice(dir, title, body); err != nil {
			fmt.Fprintln(os.Stderr, "admiral: could not post the migration notice:", err)
		}
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer cfg.Close()
	listenOn, err := resolveListen(cfg, *listen, func() (bool, error) { return cli.OfferSetup(cli.Std()) })
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listenOn)
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	lis := newListener(ln, listenOn, *listen != "")

	// Only the mode runs tailscale; a public Admiral is the one check that stops startup.
	var ts tailscale.Report
	tsMode := cfg.Current().Remote.Tailscale
	if tsMode {
		ts = tailscale.Inspect(context.Background(), port)
		if ts.Funnel {
			_ = ln.Close()
			c, _ := ts.Check(tailscale.CheckFunnel)
			return errors.New(c.Detail)
		}
	}

	// Without tmux Admiral still serves its config; there are no ships and a
	// launch is refused.
	tmuxPath, err := sessionhost.FindTmux()
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
		Tmux: tmuxPath, Socket: sock, Home: dir, Port: port, Config: cfg.Current,
		Kept: func(session string) bool { _, ok := st.KeptInbox(session); return ok },
		AutopilotGate: func(fleet config.Fleet, harness string) error {
			return autopilotgate.Check(dir, fleet, harness)
		}}}

	if err := sources.RemoveRetired(dir); err != nil {
		fmt.Fprintln(os.Stderr, "admiral: could not remove a retired plugin:", err)
	}
	supervisor := &sources.Supervisor{Home: dir, Config: cfg}
	sourceService := &sources.Service{Home: dir, Config: cfg, Sup: supervisor, Hub: hub, Ships: shipService.List}
	// The view autopilot engine kept its state here.
	_ = os.Remove(filepath.Join(dir, "autopilot.json"))
	// A ship launched but not yet listed by the host counts as running.
	allShips := func() []ships.Ship { return append(shipService.List(), shipService.Pending()...) }
	sourceService.Ships = allShips
	// A check, a lock or a reservation changing frees or fills a unit: the UI is told.
	var resourceService *resources.Service
	resourceSet := resources.New(resources.Options{Config: cfg.Current, Ships: allShips,
		OnChange: func() { resourceService.Broadcast() }})
	resourceService = &resources.Service{Set: resourceSet, Hub: hub}
	shipService.Reserve = func(names []string) (func(), string, string) {
		release, waits := resourceSet.Reserve(names)
		if waits != nil {
			return nil, waits[0].Name, waits[0].Why
		}
		return release, "", ""
	}
	supervisor.OnChange = func() { sourceService.Notify() }
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
	inboxService := &inbox.Service{Home: absDir, Hub: hub, Outcomes: outcomeLog, Kept: st, Ship: func(session string) (inbox.ShipInfo, bool) {
		if sh, ok := shipService.Get(session); ok {
			return inbox.ShipInfo{Name: sh.Name, Harness: sh.Harness, Fleet: sh.Fleet, FleetName: sh.FleetName, FleetColor: sh.FleetColor}, true
		}
		k, ok := st.KeptInbox(session)
		return inbox.ShipInfo{Name: k.Name, Harness: k.Harness, Fleet: k.Fleet, FleetName: k.FleetName, FleetColor: k.FleetColor}, ok
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
			return nil, "the ship's fleet " + strconv.Quote(fleet) + " is not in the config"
		}
		dock, ok := f.Harness(harness)
		if !ok {
			return nil, "fleet " + strconv.Quote(fleet) + " has no " + harness + " dock in the config"
		}
		return dock.Environ(), ""
	}}
	// A ship still afloat reads its answers itself: its resume text is typed at
	// its prompt once its turn has ended, and no order launches a second ship.
	inboxService.Fleet = func(key string) (inbox.FleetInfo, bool) {
		f, ok := cfg.Current().Fleet(key)
		return inbox.FleetInfo{Key: f.Key, Name: f.Name, Color: f.Color}, ok
	}
	inboxService.BuiltIns = func(key string) []string {
		f, ok := cfg.Current().Fleet(key)
		if !ok {
			return nil
		}
		return crewapi.BuiltInNames(f)
	}
	inboxService.Afloat = func(session string) bool {
		sh, ok := shipService.Get(session)
		return ok && (sh.Status == ships.StatusUnderway || sh.Status == ships.StatusNeedsYou)
	}
	inboxService.TypeResume = shipService.TypeResume
	inboxService.LaunchProposal = func(ctx context.Context, p inbox.WakeProposal) (any, int, string) {
		got, lerr := shipService.LaunchProposal(ctx, p)
		if lerr != nil {
			return nil, lerr.Status, lerr.Msg
		}
		return got, 0, ""
	}
	shipService.CaptureResume = inboxService.CaptureResume
	shipService.GitHub = func(fleet string) moor.GitHub {
		if c := sources.FleetGitHub(cfg.Current(), fleet); c != nil {
			return c
		}
		return nil
	}
	changed := supervisor.OnChange
	supervisor.OnChange = func() { changed(); inboxService.KickResume() }
	followStore, err := follow.OpenStore(dir)
	if err != nil {
		return fmt.Errorf("campaigns: %w", err)
	}
	// Campaign children answer their own orders; every other order goes to the sources.
	campaignService := &follow.Service{Store: followStore, Next: sourceService,
		Route:   func(host, nwo string) string { return github.RouterFromFleets(cfg.Current().Fleets).Route(host, nwo) },
		Clients: supervisor.GitHubClient, Ships: allShips,
		Claims: shipService.Claims, Launch: shipService.Launch}
	defer campaignService.Close()
	shipService.Orders = campaignService
	// An unreadable key file is logged and leaves push off, never replaced: devices subscribe against the key.
	vapidKey, err := push.LoadKey(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admiral: push is off: %v\n", err)
	}
	pushPublic := func() string {
		if vapidKey == nil {
			return ""
		}
		return vapidKey.Public()
	}
	guard, err := auth.New(dir, port, auth.WithBaseURL(func() string { return pairingBase(port, cfg.Current) }),
		auth.WithRemote(func() (bool, []string) {
			r := cfg.Current().Remote
			return r.Tailscale, r.AllowedLogins
		}), auth.WithPushKey(pushPublic))
	if err != nil {
		return fmt.Errorf("devices: %w", err)
	}
	var pushSinks []push.Sink
	if vapidKey != nil {
		client, err := pushClient()
		if err != nil {
			return fmt.Errorf("push: %w", err)
		}
		sender := push.NewSender(push.SenderConfig{Key: vapidKey, Client: client, Devices: pushDevices{guard}, Queue: 64, Timeout: 15 * time.Second})
		defer sender.Close()
		pushSinks = append(pushSinks, sender)
	}
	// A sink joins here: NewWatcher takes it after the notify func.
	pushFeed := &push.Feed{Ships: shipService.List, Items: inboxService.List, InboxReady: inboxService.Scanned}
	pushFeed.Watcher = push.NewWatcher(hub.Watching, func(fleet string) bool {
		for _, f := range cfg.Current().Fleets {
			if f.Key == fleet {
				return f.Notify
			}
		}
		return true
	}, append([]push.Sink{push.NewAlertSink(push.Async(hub))}, pushSinks...)...)
	inboxService.OnChange = pushFeed.InboxChanged
	shipService.OnChange = func() {
		campaignService.ShipsChanged()
		sourceService.Notify()
		bridgeService.ShipsChanged()
		resourceSet.Wake() // a ship that stopped holding a unit frees it
		resourceService.Broadcast()
		inboxService.Notify()
		pushFeed.ShipsChanged()
	}

	minter, err := actor.NewMinter(absDir)
	if err != nil {
		return err
	}
	chatService := &chat.Service{Actor: minter, Host: host, Hub: hub, Launcher: shipService.Launcher, Config: cfg.Current, Tmux: tmuxPath, Socket: sock, Home: absDir, Outcomes: outcomeLog}
	shipService.Launcher.BeforeChat = chatService.WithdrawQuestions
	chatService.OnQuestions = inboxService.Changed
	inboxService.ChatQuestions = chatService.WaitingQuestions
	inboxService.AnswerChat = chatService.AnswerQuestion
	if err := chatService.RemoveOldLayout(); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: chat: %v\n", err)
	}
	convService := &shipconv.Service{Ships: shipService, Config: cfg.Current, Tmux: tmuxPath, Socket: sock, Asking: shipService.Asking}
	// Live figures live in memory only; a restart starts empty.
	liveUsage := usage.NewStore(time.Now)
	shipService.LastLive, chatService.LastLive = liveUsage.LastLive, liveUsage.LastLive
	liveLogin := func(ship string) string {
		if sessionhost.IsChatSession(ship) {
			return chatService.Login(ship)
		}
		return shipService.Login(ship)
	}
	codexFeed := &uselive.Codex{Store: liveUsage, Login: liveLogin}
	forgetLive := func(ship string) { liveUsage.Forget(ship); codexFeed.Forget(ship) }
	shipService.ForgetLive, chatService.ForgetLive = forgetLive, forgetLive
	convService.LiveFrame = codexFeed.Frame
	liveTmux := &uselive.Tmux{Server: sessionhost.NewServer(tmuxPath, sock), Dir: liveLogin, Harness: shipService.Harness}
	claudeFeed := &uselive.Claude{Store: liveUsage, Login: liveLogin, Panes: liveTmux.ClaudePanes}
	copilotFeed := &uselive.Copilot{Store: liveUsage, Files: liveTmux.CopilotFiles}
	probeRunner := probes.NewDefault(dir)
	tails := convService.Tails()
	shipService.Tail = tails.Tail
	shipService.Questions = convService.PendingQuestions
	shipService.Approving = convService.ApprovalPending
	s := &server.Server{Path: auth.PathName, Version: version, Commit: commit, UI: web.UI(), Messages: convService.Messages(), Config: cfg,
		Hub: hub, Modules: []server.Module{shipService, sourceService, inboxService,
			&launchorders.Service{Home: absDir, Config: cfg, State: st, Ships: shipService.List, Orders: campaignService}, &crewapi.Service{Home: absDir, Config: cfg}, campaignService, bridgeService, resourceService, lockRoute, chatService, convService, &shipdiff.Service{Ships: shipService},
			&usageapi.Service{Home: absDir, Config: cfg.Current, Live: liveUsage, Ships: shipService.List, Now: time.Now, Zone: time.Local},
			&harnesscaps.Service{Home: dir, Config: cfg.Current, Runner: probeRunner}, guardRoutes{guard}, remoteRoutes{&tailscale.Route{Config: cfg.Current, Set: cfg.SetRemote, Port: port, Local: auth.IsLocal, ListenErr: lis.Err, Explicit: lis.explicit}}},
		Capabilities: []string{"ships", "sources", "inbox", "launch-orders", "bridge", "setup", "resources", "chat", "conversation", "campaigns", "ship-input", "changes", "usage"}}
	shipService.MergeGuard = s.MergeGuard
	bg, stopBackground := context.WithCancel(context.Background())
	// In the background: a harness that hangs on --version must not delay the server.
	probeRunner.Refresh(bg, cfg.Current().Fleets)
	var background sync.WaitGroup
	supervisor.Start(bg)
	go pushFeed.Run(bg)
	convService.StartLive(bg)
	// The ships' run stops first and the log closes next, while the host is
	// still up, so a written `ended` can still set its marker.
	shipsCtx, stopShips := context.WithCancel(bg)
	shipsDone := make(chan struct{})
	go func() {
		defer close(shipsDone)
		var run sync.WaitGroup
		run.Add(1)
		go func() { defer run.Done(); shipService.RunMoored(shipsCtx) }()
		shipService.Run(shipsCtx)
		run.Wait()
	}()
	configChanges, unsubscribe := cfg.Subscribe()
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-configChanges:
				moveListener(lis, cfg, hub, guard, port)
				sourceService.Notify()
				resourceSet.Refresh()
				resourceSet.Wake()
				resourceService.Broadcast()
				inboxService.KickResume()
			case <-shipsCtx.Done():
				return
			}
		}
	}()
	runs := []func(context.Context){s.WatchConfig, sourceService.Run, campaignService.Run, bridgeService.Run, resourceSet.Run, inboxService.Run, chatService.Run, tails.Run}
	if tmuxPath != "" {
		runs = append(runs, host.Run, func(ctx context.Context) { liveTmux.Run(ctx, claudeFeed.Poll, copilotFeed.Poll) })
	}
	for _, run := range runs {
		background.Add(1)
		go func() { defer background.Done(); run(bg) }()
	}
	defer func() {
		stopShips()
		<-shipsDone
		outcomeLog.Close()
		stopBackground()
		probeRunner.Close()
		probeRunner.Wait()
		background.Wait()
		supervisor.Wait()
		_ = st.Flush()
	}()
	actorGuard := &actor.Guard{
		Tokens: func(token string) (string, bool) {
			var keys []string
			for _, f := range cfg.Current().Fleets {
				keys = append(keys, f.Key)
			}
			return actor.Lookup(token, keys, chatService.ActorToken)
		},
		ShipFleet: shipService.FleetOf,
		ItemFleet: func(session, id string) (string, bool) {
			for _, it := range inboxService.List() {
				if it.Session == session && it.ID == id {
					return it.Fleet, it.Fleet != ""
				}
			}
			return "", false
		},
		SourceFleet: func(key string) (string, bool) {
			fleet, ok := sourceService.FleetOf(key)
			return fleet, ok && fleet != ""
		},
		CampaignFleet: func(id string) (string, bool) {
			f, ok := followStore.Get(id)
			return f.Fleet, ok && f.Fleet != ""
		},
	}
	srv := &http.Server{Handler: guard.Middleware(actorGuard.Middleware(s.Handler()))}
	srv.RegisterOnShutdown(func() { close(stopping) }) // a held lock would hold up the shutdown
	lis.serveWith(srv)

	fmt.Printf("Admiral is running at http://127.0.0.1:%s/\n", port)
	bindHost, _, _ := net.SplitHostPort(listenOn)
	ip, hasIP := server.LANIPv4()
	switch {
	case tsMode && ts.URL != "":
		fmt.Printf("On other devices: %s\n", ts.URL)
		if c, bad := ts.Failed(); bad {
			fmt.Printf("Warning: Tailscale mode has a failed check: %s\n", c.Detail)
		}
		fmt.Println("To pair a device, run: admiral pair")
	case tsMode:
		c, _ := ts.Failed()
		fmt.Printf("On other devices: Tailscale mode is on, but %s\n", c.Detail)
	case bindHost != "" && bindHost != "0.0.0.0" && bindHost != "::":
		fmt.Printf("On other devices: not reachable, Admiral listens only on %s (http://127.0.0.1:%s/)\n", bindHost, port)
	case hasIP:
		fmt.Printf("On other devices: http://%s:%s/\n", ip, port)
		fmt.Println("To pair a device, run: admiral pair")
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
	lis.start()
	select {
	case <-ctx.Done():
		<-drained
		return nil
	case err = <-lis.fatal:
		return err
	}
}

// moveListener applies a config change: it moves the listener when the mode changed where Admiral should
// listen, telling the browsers that move leaves behind first, then cuts the tailnet sockets the change no longer allows.
func moveListener(lis *listener, cfg *config.Manager, hub *server.Hub, guard *auth.Auth, port string) {
	c := cfg.Current()
	want := listenAddr("", c, false)
	switch {
	case lis.explicit || cfg.View().Error != nil || want == lis.Attempted():
		guard.EnforceRemote()
		return
	case want == lis.Addr(): // back to where it is bound, after a move that failed
		lis.Settle()
		guard.EnforceRemote()
		return
	}
	// The browsers whose path stops working: the LAN ones when the mode turns on, the tailnet ones when it turns off.
	paths, url := []string{"tailnet"}, ""
	if c.Remote.Tailscale {
		paths = []string{"lan"}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		url = tailscale.Inspect(ctx, port).URL
		cancel()
	} else if ip, ok := server.LANIPv4(); ok {
		url = "http://" + net.JoinHostPort(ip, port)
	}
	hub.SendPaths(paths, map[string]string{"type": "move", "url": url}, 2*time.Second)
	guard.EnforceRemote()
	lis.Move(want)
	if c.Remote.Tailscale && lis.Err() == nil {
		lis.DropOffLoopback()
		guard.CutLAN()
	}
}
