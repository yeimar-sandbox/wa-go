package whatsapp

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// WebhookEvent represents an event dispatched to registered webhooks
type WebhookEvent struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	InstanceID string `json:"instanceId"`
	Timestamp  string `json:"timestamp"`
	Data       any    `json:"data"`
}

// WebhookTarget represents a registered webhook endpoint
type WebhookTarget struct {
	URL    string
	Secret string
	Events []string
}

type EventDispatcher struct {
	httpCl    *http.Client
	mu        sync.RWMutex
	targets   map[string][]WebhookTarget     // instanceID -> webhooks
	wsClients map[string][]chan WebhookEvent // instanceID -> websocket channels
}

func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{
		httpCl:    &http.Client{Timeout: 10 * time.Second},
		targets:   make(map[string][]WebhookTarget),
		wsClients: make(map[string][]chan WebhookEvent),
	}
}

func (d *EventDispatcher) Register(instanceID string, target WebhookTarget) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.targets[instanceID] = append(d.targets[instanceID], target)
}

func (d *EventDispatcher) Unregister(instanceID, url string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	targets := d.targets[instanceID]
	filtered := make([]WebhookTarget, 0, len(targets))
	for _, t := range targets {
		if t.URL != url {
			filtered = append(filtered, t)
		}
	}
	d.targets[instanceID] = filtered
}

func (d *EventDispatcher) SetTargets(instanceID string, targets []WebhookTarget) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.targets[instanceID] = targets
}

func (d *EventDispatcher) SubscribeWs(instanceID string) <-chan WebhookEvent {
	ch := make(chan WebhookEvent, 100)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.wsClients[instanceID] = append(d.wsClients[instanceID], ch)
	return ch
}

func (d *EventDispatcher) UnsubscribeWs(instanceID string, ch <-chan WebhookEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	clients := d.wsClients[instanceID]
	filtered := make([]chan WebhookEvent, 0, len(clients))
	for _, c := range clients {
		if c != ch {
			filtered = append(filtered, c)
		} else {
			close(c)
		}
	}
	d.wsClients[instanceID] = filtered
}

func (d *EventDispatcher) Dispatch(instanceID, eventType string, data any) {
	evt := WebhookEvent{
		ID:         uuid.New().String(),
		Type:       eventType,
		InstanceID: instanceID,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Data:       data,
	}

	// Hold the read lock through the whole send loop so a concurrent
	// UnsubscribeWs cannot close(c) while we are sending into it.
	// WS sends are non-blocking (select/default) and webhook sends are
	// goroutine'd, so the critical section stays short.
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, ch := range d.wsClients[instanceID] {
		select {
		case ch <- evt:
		default:
			// channel full, drop event
		}
	}

	for _, target := range d.targets[instanceID] {
		if !d.matchesEvent(target.Events, eventType) {
			continue
		}
		go d.send(target, evt)
	}
}

func (d *EventDispatcher) matchesEvent(subscribed []string, eventType string) bool {
	if len(subscribed) == 0 {
		return true // subscribed to all
	}
	for _, e := range subscribed {
		if e == eventType || e == "*" {
			return true
		}
		// match prefix: "message.*" matches "message.received"
		if strings.HasSuffix(e, ".*") && strings.HasPrefix(eventType, e[:len(e)-2]) {
			return true
		}
	}
	return false
}

func (d *EventDispatcher) send(target WebhookTarget, evt WebhookEvent) {
	body, err := json.Marshal(evt)
	if err != nil {
		slog.Error("webhook marshal failed", "error", err)
		return
	}

	req, err := http.NewRequest("POST", target.URL, bytes.NewReader(body))
	if err != nil {
		slog.Error("webhook request failed", "url", target.URL, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-ID", evt.ID)
	req.Header.Set("X-Webhook-Event", evt.Type)

	if target.Secret != "" {
		mac := hmac.New(sha256.New, []byte(target.Secret))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Webhook-Signature", "sha256="+sig)
	}

	resp, err := d.httpCl.Do(req)
	if err != nil {
		slog.Error("webhook delivery failed", "url", target.URL, "error", err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Warn("webhook returned error", "url", target.URL, "status", resp.StatusCode)
	}
}
