// Command remux supervises a Mac's tmux sessions - and the coding agents
// living in them - from a phone.
//
// The whole product ships as this one binary: the phone UI is compiled in with
// go:embed and the Tailscale node is embedded with tsnet, so there is nothing
// else to install and nothing to configure.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/viminizer/remux/internal/api"
	"github.com/viminizer/remux/internal/config"
	gh "github.com/viminizer/remux/internal/github"
	"github.com/viminizer/remux/internal/push"
	"github.com/viminizer/remux/internal/titler"
	"github.com/viminizer/remux/internal/tmux"
	"github.com/viminizer/remux/internal/tsnode"
	"github.com/viminizer/remux/internal/web"
)

// version is overridden at build time by scripts/build.sh.
var version = "dev"

func main() {
	log.SetFlags(log.Ltime)

	// Subcommands come before flag parsing so `remux install` does not have
	// to look like a flag.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			exit(cmdInstall())
		case "uninstall":
			exit(cmdUninstall())
		case "restart":
			exit(cmdRestart())
		case "status":
			exit(cmdStatus())
		case "help", "-h", "--help":
			usage()
			return
		case "version", "--version":
			fmt.Println("remux " + version)
			return
		}
	}

	var (
		local    = flag.Bool("local", false, "serve plain HTTP on 127.0.0.1 with no tailnet and no auth (for UI work)")
		hostname = flag.String("hostname", "", "tsnet node name (default remux)")
		lines    = flag.Int("lines", 0, "capture depth in lines (default 400)")
		poll     = flag.Int("poll", 0, "pane poll interval in ms (default 400)")
		port     = flag.Int("port", 0, "port for --local (default 7399)")
	)
	flag.Usage = usage
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Printf("config: %v (using defaults)", err)
	}
	if *hostname != "" {
		cfg.Hostname = *hostname
	}
	if *lines > 0 {
		cfg.Lines = *lines
	}
	if *poll > 0 {
		cfg.PollMS = *poll
	}
	if *port > 0 {
		cfg.Port = *port
	}

	api.Version = version

	if err := run(cfg, *local); err != nil {
		fmt.Fprintln(os.Stderr, "\nremux: "+err.Error())
		os.Exit(1)
	}
}

func run(cfg *config.Config, local bool) error {
	// Preflight before anything else: the boring failures (no tmux, port in
	// use, unwritable config dir) are the ones most easily mistaken for bugs.
	pf := preflight(cfg, local)
	pf.Print()
	if !pf.OK() {
		return fmt.Errorf("preflight failed - fix the ✗ lines above and run again")
	}

	tm := tmux.New()

	srv := api.NewServer(cfg, tm, web.Handler())
	if audit, err := api.OpenAuditLog(); err == nil {
		srv.Audit = audit
		defer audit.Close()
	} else {
		log.Printf("audit log unavailable: %v", err)
	}

	// The GitHub screen. gh being absent or logged out is not a startup
	// failure - the screen says so and everything else keeps working - so
	// the poller starts either way and reports what it finds.
	ghClient := gh.New()
	// Read through the server so an edit from the phone applies on the next
	// poll rather than on the next restart.
	ghClient.Ignored = func() gh.Ignored { return srv.IgnoredChecks() }
	srv.GHClient = ghClient
	srv.Match = gh.NewMatcher()
	srv.GH = &gh.Poller{
		Client:    ghClient,
		Interval:  cfg.GitHubPoll(),
		Watchlist: func() []string { return srv.Watchlist() },
	}

	if store, err := push.Open(); err == nil {
		srv.Push = store
	} else {
		log.Printf("push unavailable: %v", err)
	}

	// The GitHub watcher hangs off the poller rather than running a loop of
	// its own: the read is already happening, so a notification costs
	// nothing extra. Same local-mode exclusion as the pane watcher, and for
	// the same reason - a development server shares ~/.config/remux with the
	// installed service and would double every notification.
	if srv.Push != nil && !local {
		ghw := push.NewGitHubWatcher(srv.Push)
		ghw.Enabled = srv.NotifyCI
		srv.GH.OnSnapshot = ghw.OnSnapshot
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Not in local mode. The push store is a single directory in ~/.config,
	// so a development server opens the very subscriptions the installed
	// service is already watching: two sweeps of the real workspace, and the
	// phone notified twice about the same pane. AGENTS.md tells anyone
	// building this to run --local against the live tmux, so this is the
	// normal case, not a corner.
	if srv.Push != nil && !local {
		w := push.NewWatcher(tm, srv.Push)
		w.NotifyWaiting = srv.NotifyWaiting
		w.NotifyDone = srv.NotifyDone
		go w.Run(ctx)
	}

	// Naming panes writes to the real workspace on remux's own initiative,
	// rather than because a phone tapped something - the first thing here
	// that does. It is a benign write: one user option per pane, no cursor
	// moved, no geometry touched, and scripts/laptop-invariant.sh checks
	// exactly that.
	//
	// Unlike the two watchers above, this runs under --local as well. Their
	// exclusion is about a development server doubling a notification, which
	// is a real annoyance; two processes writing the same glyph derived from
	// the same screen is not, and the write is skipped when the value already
	// matches. Excluding it would mean the one feature whose acceptance is
	// "it works against the panes running right now" could not be tried
	// against them.
	//
	// The naming half spends money, so it carries a switch the phone can
	// reach. The glyph half does not and has none.
	namer := titler.New(tm)
	namer.Chain = titler.Chain()
	// srv.NamePanes, not a closure over cfg.NamePanes: this is read from the
	// WatchPanes goroutine every couple of seconds and the settings handler
	// writes it, so the read has to take the same lock as the write.
	namer.Enabled = srv.NamePanes
	// Away means "nobody is reading this", not "he is not at this keyboard".
	// The HID timer alone answers the second, and answering only that turned
	// the naming off exactly when it is most wanted: measured on this laptop,
	// a stretch of working entirely from the phone left HIDIdleTime climbing
	// past two and a half hours with every pane unnamed. remux is the tool for
	// being away from the Mac, so a phone with the app open has to count as
	// being here. The HID timer stays as the backstop it was written to be -
	// an agent grinding overnight with nobody looking at all.
	namer.Away = func() bool { return !srv.Watching() && titler.HIDAway() }
	srv.OnTree = namer.OnTree

	go srv.GH.Run(ctx)
	// The one always-on pass over the workspace. Pane/repo matching runs here
	// rather than inside a handler, because it reads the filesystem and a read
	// under ~/Desktop from a LaunchAgent macOS has not granted access to
	// blocks rather than failing.
	go srv.WatchPanes(ctx, cfg.TreePoll())

	if local {
		return serveLocal(ctx, srv, cfg)
	}
	return serveTailnet(ctx, srv, cfg)
}

// serveLocal is the development path: plain HTTP on loopback, no tailnet, no
// auth check. The listener is loopback-only, which is what makes skipping the
// identity check safe here.
func serveLocal(ctx context.Context, srv *api.Server, cfg *config.Config) error {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	hs := &http.Server{Addr: addr, Handler: srv.Handler()}

	fmt.Printf("\n  remux %s  ·  local mode\n", version)
	fmt.Printf("  http://%s\n\n", addr)
	if !web.Built() {
		fmt.Println("  note: the UI is not built into this binary. Run scripts/build.sh.")
	}

	go func() {
		<-ctx.Done()
		shutdown(hs)
	}()
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// serveTailnet is the real path: the node joins the tailnet and serves HTTPS
// on it, with WhoIs deciding who gets through.
func serveTailnet(ctx context.Context, srv *api.Server, cfg *config.Config) error {
	dir, err := config.EnsureDir()
	if err != nil {
		return err
	}

	fmt.Printf("\n  remux %s  ·  joining your tailnet as %q\n", version, cfg.Hostname)
	fmt.Println("  (first run only: a login URL will appear below - open it once)")

	node, err := tsnode.Start(ctx, cfg.Hostname, dir, printLoginURL)
	if err != nil {
		return err
	}
	defer node.Close()

	srv.Auth = &api.Auth{
		LC:         node.LC,
		AllowLogin: cfg.AllowLogin,
		OnPin: func(login string) {
			cfg.AllowLogin = login
			if err := config.Save(cfg); err != nil {
				log.Printf("could not persist the allowed identity: %v", err)
				return
			}
			fmt.Printf("\n  allowed identity pinned to %s (saved)\n", login)
		},
	}

	ln, err := node.ListenTLS(":443")
	if err != nil {
		return fmt.Errorf("%s", tsnode.Explain(err))
	}
	defer ln.Close()

	url := node.URL()
	fmt.Printf("\n  ready:  %s\n", url)
	if cfg.AllowLogin != "" {
		fmt.Printf("  allowed identity: %s\n", cfg.AllowLogin)
	} else {
		fmt.Println("  allowed identity: the first tailnet user to connect")
	}
	fmt.Println()
	printQR(url)
	fmt.Println("\n  Scan that on your phone, then Chrome menu → Add to Home screen.")
	fmt.Println("  Keeping the Mac awake is still your job: caffeinate -dims")

	hs := &http.Server{Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		shutdown(hs)
	}()
	if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// printLoginURL presents the one-time enrolment step.
//
// This is the only thing standing between a fresh install and a working
// service, and under launchd it is read out of a log file, so it is framed
// rather than logged in passing.
func printLoginURL(url string) {
	fmt.Println()
	fmt.Println("  ┌──────────────────────────────────────────────────────────┐")
	fmt.Println("  │  One-time setup: this Mac needs to join your tailnet.    │")
	fmt.Println("  └──────────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Println("  Open this and approve the device:")
	fmt.Printf("\n      %s\n\n", url)
	fmt.Println("  remux will finish starting by itself once you have. This is asked")
	fmt.Println("  once - every later start is silent.")
	fmt.Println()
}

func shutdown(hs *http.Server) {
	c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = hs.Shutdown(c)
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "remux: "+err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func usage() {
	fmt.Fprint(os.Stderr, `remux - supervise your Mac's tmux and coding agents from your phone

usage:
  remux                run on your tailnet (https://remux.<tailnet>.ts.net)
  remux --local        run on 127.0.0.1:7399, no tailnet, no auth - for UI work
  remux install        write and load the launchd agent; starts at login
  remux uninstall      unload and remove it
  remux restart        reload after replacing the binary
  remux status         preflight, reachability and tailnet identity, then exit

flags:
  --local              serve plain HTTP on loopback
  --hostname NAME      tsnet node name (default remux)
  --port N             port for --local (default 7399)
  --lines N            capture depth (default 400)
  --poll MS            pane poll interval (default 400)
`)
}
