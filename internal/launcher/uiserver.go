package launcher

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// Flags that make Copilot a server, or choose how it listens, or are refused beside --ui-server.
var uiServerConflicts = []string{"--ui-server", "--server", "--headless", "--stdio", "--connect", "--host", "--port"}

// uiServerFlags is what a Copilot ship's observer needs: the server on a loopback port Admiral chose.
// It is nil when the order or the dock already picks a session, a port or a server mode.
func uiServerFlags(req Request, dock config.Harness, port int) []string {
	if req.Harness != "copilot" || port <= 0 || choosesSession("copilot", dock.Args) || choosesSession("copilot", req.ExtraArgs) {
		return nil
	}
	for _, args := range [][]string{dock.Args, req.ExtraArgs} {
		for _, a := range args {
			if a == "--" {
				break
			}
			name, _, _ := strings.Cut(a, "=")
			for _, f := range uiServerConflicts {
				if name == f {
					return nil
				}
			}
		}
	}
	return []string{"--ui-server", "--port", strconv.Itoa(port)}
}

type gateKey struct {
	path string
	size int64
	mod  time.Time
}

type gateAnswer struct {
	ok bool
	at time.Time
}

// gateTTL bounds a stale answer: Copilot's launcher binary updates the real build in its own cache, so the
// resolved path, size and mtime do not change when the version does.
const gateTTL = 30 * time.Minute

var (
	gateMu    sync.Mutex
	gateCache = map[gateKey]gateAnswer{}
)

// uiServerSupported asks the binary whether it accepts --ui-server, with no model call: --version exits 0 on a
// build that knows the flag and 1 on one that does not. The answer holds until the binary changes or gateTTL passes.
func uiServerSupported(bin string) bool {
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return false
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return false
	}
	k := gateKey{resolved, st.Size(), st.ModTime()}
	gateMu.Lock()
	defer gateMu.Unlock()
	if a, asked := gateCache[k]; asked && time.Since(a.at) < gateTTL {
		return a.ok
	}
	port, ok := freeLoopbackPort()
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ok = exec.CommandContext(ctx, resolved, "--ui-server", "--port", strconv.Itoa(port), "--version").Run() == nil
	gateCache[k] = gateAnswer{ok, time.Now()}
	return ok
}

// freeLoopbackPort is a port nothing listens on now; another process can still take it before Copilot binds,
// which leaves the ship without a server and on its file.
func freeLoopbackPort() (int, bool) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, false
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, true
}

// uiServerLaunchFlags is the flags for a ship launch, or none when the binary does not accept them.
func uiServerLaunchFlags(req Request, dock config.Harness, exe string) []string {
	port, ok := freeLoopbackPort()
	if !ok {
		return nil
	}
	flags := uiServerFlags(req, dock, port)
	if flags == nil || !uiServerSupported(exe) {
		return nil
	}
	return flags
}
