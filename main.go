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
	"github.com/viminizer/remux/internal/push"
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

	if store, err := push.Open(); err == nil {
		srv.Push = store
	} else {
		log.Printf("push unavailable: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if srv.Push != nil {
		w := push.NewWatcher(tm, srv.Push)
		w.NotifyWaiting = func() bool { return cfg.NotifyWaiting }
		w.NotifyDone = func() bool { return cfg.NotifyDone }
		go w.Run(ctx)
	}

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

	node, err := tsnode.Start(ctx, cfg.Hostname, dir)
	if err != nil {
		return err
	}
	defer node.Close()

	srv.Auth = &api.Auth{LC: node.LC, AllowLogin: cfg.AllowLogin}

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
