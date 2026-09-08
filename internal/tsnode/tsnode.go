// Package tsnode embeds a Tailscale node in the remux process.
//
// tsnet runs a full Tailscale node in userspace, so remux joins the tailnet as
// its own device with no tailscaled, no root, no system extension and no
// Tailscale app on the Mac. Two things fall out of that and they are the
// reason the design chose it:
//
//   - ListenTLS announces only on the tailnet. Nothing listens on a real
//     network interface, so there is no LAN port, no firewall rule and no port
//     forward - unreachable from outside the tailnet by construction.
//   - Tailscale issues a real cert for remux.<tailnet>.ts.net, which gives a
//     secure context. That is what makes the Android home-screen install and
//     the service worker work at all.
//
// ListenFunnel is deliberately not used: this is a remote-control plane for a
// machine running agents with filesystem access, and it stays on the tailnet.
package tsnode

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

type Node struct {
	Srv      *tsnet.Server
	LC       *local.Client
	DNSName  string // remux.tailXXXX.ts.net
	Hostname string
}

// Start brings up the node and blocks until it is enrolled.
//
// On first run tsnet prints a login URL to stdout; after that the state in dir
// is enough and every later start is silent.
func Start(ctx context.Context, hostname, dir string) (*Node, error) {
	stateDir := filepath.Join(dir, "tsnet")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	srv := &tsnet.Server{
		Hostname: hostname,
		Dir:      stateDir,
		Logf:     func(string, ...any) {}, // tsnet is very chatty; keep stdout for our own output
	}

	if _, err := srv.Up(ctx); err != nil {
		srv.Close()
		return nil, fmt.Errorf("tailscale node did not come up: %w", err)
	}

	lc, err := srv.LocalClient()
	if err != nil {
		srv.Close()
		return nil, fmt.Errorf("local client: %w", err)
	}

	n := &Node{Srv: srv, LC: lc, Hostname: hostname}

	if st, err := lc.StatusWithoutPeers(ctx); err == nil && st.Self != nil {
		n.DNSName = strings.TrimSuffix(st.Self.DNSName, ".")
	}
	return n, nil
}

// URL is the address to open on the phone.
func (n *Node) URL() string {
	if n.DNSName == "" {
		return ""
	}
	return "https://" + n.DNSName
}

// ListenTLS listens on the tailnet with a Tailscale-issued certificate.
//
// This is the call that fails when the tailnet has MagicDNS or HTTPS
// Certificates switched off, and the error it returns is opaque. Explain()
// turns that into an instruction rather than letting it reach the user raw.
func (n *Node) ListenTLS(addr string) (net.Listener, error) {
	return n.Srv.ListenTLS("tcp", addr)
}

func (n *Node) Close() error { return n.Srv.Close() }

// Explain converts a ListenTLS failure into something actionable.
//
// This is the most likely first-run failure, and the raw error names neither
// the toggle nor the page it lives on.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	low := strings.ToLower(msg)

	switch {
	case strings.Contains(low, "https must be enabled"),
		strings.Contains(low, "no https"),
		strings.Contains(low, "cert"),
		strings.Contains(low, "magicdns"),
		strings.Contains(low, "acme"),
		strings.Contains(low, "tls"):
		return "Tailscale could not issue an HTTPS certificate for this node.\n\n" +
			"Two things must be on in your tailnet, and both are one click:\n\n" +
			"  1. MagicDNS            https://login.tailscale.com/admin/dns\n" +
			"  2. HTTPS Certificates  https://login.tailscale.com/admin/dns  (same page, lower down)\n\n" +
			"Turn both on, then run remux again. Nothing else needs changing.\n\n" +
			"Original error: " + msg
	}
	return msg
}
