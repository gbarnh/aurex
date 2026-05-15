package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type PushManager struct {
	mu            sync.Mutex
	subscriptions []webpush.Subscription
	publicKey     string
	privateKey    string
	subscriberURL string // VAPID subject — usually a mailto:
	storePath     string // disk persistence; "" disables
}

func NewPushManager(publicKey, privateKey, storePath string) *PushManager {
	p := &PushManager{
		publicKey:  publicKey,
		privateKey: privateKey,
		// VAPID `sub` claim. FCM rejects mailto: URIs that point to bogus
		// domains (e.g. localhost) with 403, even though the spec only says
		// "valid mailto: or https:". example.com is IANA-reserved and accepted.
		subscriberURL: "mailto:aurex@example.com",
		storePath:     storePath,
	}
	if err := p.load(); err != nil {
		log.Printf("push: load subscriptions: %v", err)
	}
	return p
}

func (p *PushManager) load() error {
	if p.storePath == "" {
		return nil
	}
	data, err := os.ReadFile(p.storePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var subs []webpush.Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	p.mu.Lock()
	p.subscriptions = subs
	p.mu.Unlock()
	log.Printf("push: loaded %d subscription(s) from %s", len(subs), p.storePath)
	return nil
}

// persist writes the current subscription list to disk. Caller must NOT hold p.mu.
func (p *PushManager) persist() {
	if p.storePath == "" {
		return
	}
	p.mu.Lock()
	subs := make([]webpush.Subscription, len(p.subscriptions))
	copy(subs, p.subscriptions)
	p.mu.Unlock()

	data, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		log.Printf("push: marshal subs: %v", err)
		return
	}
	if dir := filepath.Dir(p.storePath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(p.storePath, data, 0o600); err != nil {
		log.Printf("push: write subs: %v", err)
	}
}

func (p *PushManager) PublicKey() string {
	return p.publicKey
}

func (p *PushManager) AddSubscription(sub webpush.Subscription) {
	p.mu.Lock()
	for _, existing := range p.subscriptions {
		if existing.Endpoint == sub.Endpoint {
			p.mu.Unlock()
			return
		}
	}
	p.subscriptions = append(p.subscriptions, sub)
	p.mu.Unlock()
	p.persist()
}

type NotificationPayload struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	SessionID string `json:"sessionId"`
	Tag       string `json:"tag"`
}

// NotifyResult summarizes a Notify call so callers (e.g. the test endpoint)
// can tell the user whether the push actually went out and where it failed.
type NotifyResult struct {
	Total    int      `json:"total"`
	Sent     int      `json:"sent"`
	Failed   int      `json:"failed"`
	Pruned   int      `json:"pruned"`
	LastErr  string   `json:"lastError,omitempty"`
	Statuses []int    `json:"statuses,omitempty"`
}

// Notify sends a push to every registered subscription. Best-effort — failures are logged
// and the subscription is pruned if the endpoint is permanently gone.
func (p *PushManager) Notify(payload NotificationPayload) NotifyResult {
	res := NotifyResult{}
	body, err := json.Marshal(payload)
	if err != nil {
		res.LastErr = "marshal: " + err.Error()
		log.Printf("push: marshal payload: %v", err)
		return res
	}

	p.mu.Lock()
	subs := make([]webpush.Subscription, len(p.subscriptions))
	copy(subs, p.subscriptions)
	p.mu.Unlock()
	res.Total = len(subs)

	var dead []string
	for _, sub := range subs {
		s := sub
		resp, err := webpush.SendNotification(body, &s, &webpush.Options{
			TTL:             30,
			Subscriber:      p.subscriberURL,
			VAPIDPublicKey:  p.publicKey,
			VAPIDPrivateKey: p.privateKey,
		})
		if err != nil {
			res.Failed++
			res.LastErr = err.Error()
			log.Printf("push: send: %v", err)
			continue
		}
		if resp != nil {
			res.Statuses = append(res.Statuses, resp.StatusCode)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				res.Sent++
			} else {
				res.Failed++
				res.LastErr = fmt.Sprintf("push service returned %d", resp.StatusCode)
				log.Printf("push: endpoint returned %d", resp.StatusCode)
			}
			// 404/410: endpoint gone. 403: VAPID JWT rejected — for aurex's
			// single-key model this means the subscription was minted against
			// an older VAPID pair we no longer have, so it can never recover.
			// Prune it so the test endpoint stops counting it as a failure.
			if resp.StatusCode == 403 || resp.StatusCode == 404 || resp.StatusCode == 410 {
				dead = append(dead, sub.Endpoint)
			}
			resp.Body.Close()
		}
	}

	if len(dead) > 0 {
		p.mu.Lock()
		kept := p.subscriptions[:0]
		for _, s := range p.subscriptions {
			if !contains(dead, s.Endpoint) {
				kept = append(kept, s)
			}
		}
		p.subscriptions = kept
		p.mu.Unlock()
		res.Pruned = len(dead)
		p.persist()
	}
	return res
}

// SubscriptionCount returns how many devices are currently subscribed.
func (p *PushManager) SubscriptionCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.subscriptions)
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// SubscriptionFromJSON parses the body POSTed by the browser into a webpush.Subscription.
func SubscriptionFromJSON(data []byte) (webpush.Subscription, error) {
	var sub webpush.Subscription
	if err := json.Unmarshal(data, &sub); err != nil {
		return sub, fmt.Errorf("parse subscription: %w", err)
	}
	if sub.Endpoint == "" {
		return sub, fmt.Errorf("subscription has empty endpoint")
	}
	return sub, nil
}
