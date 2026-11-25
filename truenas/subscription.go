package truenas

import (
	"fmt"
	"strings"
	"sync"
)

// SubscriptionEvent is a TrueNAS middleware collection event. Event fields
// are intentionally exposed only to the bounded operation consuming them;
// the client never logs their contents.
type SubscriptionEvent struct {
	Collection string
	Fields     map[string]interface{}
	Error      *APIError
}

// Subscription represents one active TrueNAS middleware subscription.
// Callers must close it when they stop consuming events.
type Subscription struct {
	client *Client
	name   string
	events chan SubscriptionEvent

	mu     sync.Mutex
	closed bool
	err    error
}

// Subscribe registers a TrueNAS collection subscription. The name must be
// the complete middleware subscription name, including any encoded filter
// parameters, for example app.container_log_follow:{...}.
func (c *Client) Subscribe(name string) (*Subscription, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("subscription name cannot be empty")
	}
	if len(name) > 64*1024 {
		return nil, fmt.Errorf("subscription name exceeds the 65536-byte safety limit")
	}
	if err := c.ensureAuthenticated(); err != nil {
		return nil, err
	}

	subscription := &Subscription{
		client: c,
		name:   name,
		events: make(chan SubscriptionEvent, 256),
	}

	c.subscriptionsMu.Lock()
	if c.subscriptions == nil {
		c.subscriptions = make(map[*Subscription]struct{})
	}
	c.subscriptions[subscription] = struct{}{}
	c.subscriptionsMu.Unlock()

	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn == nil {
		c.removeSubscription(subscription)
		return nil, fmt.Errorf("not connected")
	}

	requestID := fmt.Sprintf("sub-%d", c.requestID.Add(1))
	request := subscriptionRequest(requestID, name)
	c.writeMu.Lock()
	err := conn.WriteJSON(request)
	c.writeMu.Unlock()
	if err != nil {
		c.removeSubscription(subscription)
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}

	return subscription, nil
}

// Events returns the bounded event channel. It is closed when the caller
// closes the subscription or the underlying connection is lost.
func (s *Subscription) Events() <-chan SubscriptionEvent {
	return s.events
}

// Err reports a terminal subscription error after Events has closed.
func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close unsubscribes and releases the subscription. It is safe to call more
// than once.
func (s *Subscription) Close() error {
	if s == nil {
		return nil
	}
	s.client.removeSubscription(s)
	s.client.sendUnsubscribe(s.name)
	s.finish(nil)
	return nil
}

func (c *Client) dispatchSubscription(event SubscriptionEvent) {
	c.subscriptionsMu.Lock()
	subscriptions := make([]*Subscription, 0, len(c.subscriptions))
	for subscription := range c.subscriptions {
		if strings.HasPrefix(event.Collection, subscription.name) || strings.HasPrefix(subscription.name, event.Collection) {
			subscriptions = append(subscriptions, subscription)
		}
	}
	c.subscriptionsMu.Unlock()

	for _, subscription := range subscriptions {
		subscription.deliver(event)
	}
}

func (c *Client) removeSubscription(subscription *Subscription) {
	c.subscriptionsMu.Lock()
	delete(c.subscriptions, subscription)
	c.subscriptionsMu.Unlock()
}

func (c *Client) failSubscriptions(err error) {
	c.subscriptionsMu.Lock()
	subscriptions := make([]*Subscription, 0, len(c.subscriptions))
	for subscription := range c.subscriptions {
		subscriptions = append(subscriptions, subscription)
	}
	c.subscriptions = make(map[*Subscription]struct{})
	c.subscriptionsMu.Unlock()

	for _, subscription := range subscriptions {
		subscription.finish(err)
	}
}

func (s *Subscription) deliver(event SubscriptionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.events <- event:
	default:
		s.closed = true
		s.err = fmt.Errorf("subscription event buffer is full")
		close(s.events)
	}
}

func (s *Subscription) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.err = err
	close(s.events)
}

func (c *Client) sendUnsubscribe(name string) {
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn == nil {
		return
	}
	c.writeMu.Lock()
	_ = conn.WriteJSON(unsubscribeRequest(name))
	c.writeMu.Unlock()
}

func subscriptionRequest(id, name string) map[string]interface{} {
	return map[string]interface{}{"id": id, "msg": "sub", "name": name}
}

func unsubscribeRequest(name string) map[string]interface{} {
	return map[string]interface{}{"msg": "unsub", "name": name}
}
