// Package push notifies the phone when an agent blocks on an answer.
//
// Without this, Kevin has to open the app to discover an agent is stuck, which
// defeats the point of the product. This is also the one part of remux that
// leaves the tailnet: push traffic goes through the browser's push service, so
// the payload deliberately carries no pane content.
package push

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/viminizer/remux/internal/config"
)

// Keys is the VAPID keypair. The private key is a real credential and is
// stored with owner-only permissions; it must never be committed.
type Keys struct {
	Public  string `json:"publicKey"`
	Private string `json:"privateKey"`
}

// Subscription is one browser push endpoint.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	AddedAt int64 `json:"addedAt"`
}

// Store holds the keypair and the registered subscriptions, and remembers
// which pane the phone currently has open.
type Store struct {
	mu    sync.Mutex
	dir   string
	keys  Keys
	subs  []Subscription
	focus string
}

func Open() (*Store, error) {
	dir, err := config.EnsureDir()
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	if err := s.loadKeys(); err != nil {
		return nil, err
	}
	s.loadSubs()
	return s, nil
}

func (s *Store) keysPath() string { return filepath.Join(s.dir, "vapid.json") }
func (s *Store) subsPath() string { return filepath.Join(s.dir, "subscriptions.json") }

// loadKeys reads the VAPID keypair, generating it on first run.
func (s *Store) loadKeys() error {
	b, err := os.ReadFile(s.keysPath())
	if err == nil {
		return json.Unmarshal(b, &s.keys)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return err
	}
	s.keys = Keys{Public: pub, Private: priv}
	out, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.keysPath(), append(out, '\n'), 0o600)
}

func (s *Store) loadSubs() {
	b, err := os.ReadFile(s.subsPath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.subs)
}

func (s *Store) saveSubs() error {
	b, err := json.MarshalIndent(s.subs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.subsPath(), append(b, '\n'), 0o600)
}

// PublicKey is handed to the browser so it can create a subscription.
func (s *Store) PublicKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys.Public
}

// Add registers a subscription, replacing any earlier one with the same
// endpoint so re-opening the app does not duplicate notifications.
func (s *Store) Add(sub Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub.AddedAt = time.Now().UnixMilli()
	for i, existing := range s.subs {
		if existing.Endpoint == sub.Endpoint {
			s.subs[i] = sub
			return s.saveSubs()
		}
	}
	s.subs = append(s.subs, sub)
	return s.saveSubs()
}

func (s *Store) Remove(endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.subs[:0]
	for _, sub := range s.subs {
		if sub.Endpoint != endpoint {
			out = append(out, sub)
		}
	}
	s.subs = out
	return s.saveSubs()
}

func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

// SetFocus records the pane the phone is currently showing, so a notification
// about that pane can be suppressed.
func (s *Store) SetFocus(pane string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.focus = pane
}

func (s *Store) Focus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focus
}

// Payload is what reaches the lock screen.
//
// It names the pane but never quotes it: this message passes through the
// browser vendor's push servers, so no agent output goes into it. The app
// fetches the real screen after the tap.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Pane  string `json:"pane"`
	Tag   string `json:"tag"`
}

// Send delivers a payload to every registered subscription.
//
// Endpoints that report 404 or 410 are gone for good (the browser dropped the
// subscription), so they are pruned rather than retried forever.
func (s *Store) Send(p Payload) error {
	s.mu.Lock()
	subs := append([]Subscription(nil), s.subs...)
	keys := s.keys
	s.mu.Unlock()

	if len(subs) == 0 {
		return nil
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}

	var dead []string
	for _, sub := range subs {
		ws := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys: webpush.Keys{
				P256dh: sub.Keys.P256dh,
				Auth:   sub.Keys.Auth,
			},
		}
		resp, err := webpush.SendNotification(body, ws, &webpush.Options{
			TTL:             120,
			VAPIDPublicKey:  keys.Public,
			VAPIDPrivateKey: keys.Private,
			Subscriber:      "remux@localhost",
		})
		if err != nil {
			continue
		}
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			dead = append(dead, sub.Endpoint)
		}
		resp.Body.Close()
	}
	for _, e := range dead {
		_ = s.Remove(e)
	}
	return nil
}
