package truenas

import (
	"testing"
	"time"
)

func TestSubscriptionDispatchAndClose(t *testing.T) {
	client := &Client{subscriptions: make(map[*Subscription]struct{})}
	subscription := &Subscription{client: client, name: "app.container_log_follow:{\"container_id\":\"abc\"}", events: make(chan SubscriptionEvent, 1)}
	client.subscriptions[subscription] = struct{}{}

	client.dispatchSubscription(SubscriptionEvent{Collection: "app.container_log_follow:{\"container_id\":\"abc\"}", Fields: map[string]interface{}{"data": "Listening for Jobs"}})
	select {
	case event := <-subscription.Events():
		if event.Fields["data"] != "Listening for Jobs" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription event was not delivered")
	}

	if err := subscription.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-subscription.Events(); ok {
		t.Fatal("subscription channel remained open")
	}
}

func TestSubscriptionDispatchDoesNotCrossTalk(t *testing.T) {
	client := &Client{subscriptions: make(map[*Subscription]struct{})}
	matching := &Subscription{client: client, name: "app.container_log_follow:{\"container_id\":\"abc\"}", events: make(chan SubscriptionEvent, 1)}
	other := &Subscription{client: client, name: "app.container_log_follow:{\"container_id\":\"xyz\"}", events: make(chan SubscriptionEvent, 1)}
	client.subscriptions[matching] = struct{}{}
	client.subscriptions[other] = struct{}{}

	client.dispatchSubscription(SubscriptionEvent{Collection: matching.name, Fields: map[string]interface{}{"data": "line"}})
	select {
	case <-matching.Events():
	case <-time.After(time.Second):
		t.Fatal("matching subscription did not receive event")
	}
	select {
	case event := <-other.Events():
		t.Fatalf("unrelated subscription received event: %#v", event)
	default:
	}
}

func TestSubscriptionFailureClosesEvents(t *testing.T) {
	client := &Client{subscriptions: make(map[*Subscription]struct{})}
	subscription := &Subscription{client: client, name: "app.container_log_follow:test", events: make(chan SubscriptionEvent, 1)}
	client.subscriptions[subscription] = struct{}{}
	client.failSubscriptions(testError("connection lost"))

	if _, ok := <-subscription.Events(); ok {
		t.Fatal("failed subscription channel remained open")
	}
	if subscription.Err() == nil {
		t.Fatal("failed subscription did not retain terminal error")
	}
}

func TestSubscriptionWireMessages(t *testing.T) {
	subscribe := subscriptionRequest("sub-1", "app.container_log_follow:{...}")
	if subscribe["id"] != "sub-1" || subscribe["msg"] != "sub" || subscribe["name"] != "app.container_log_follow:{...}" {
		t.Fatalf("unexpected subscribe message: %#v", subscribe)
	}
	unsub := unsubscribeRequest("app.container_log_follow:{...}")
	if unsub["msg"] != "unsub" || unsub["name"] != "app.container_log_follow:{...}" {
		t.Fatalf("unexpected unsubscribe message: %#v", unsub)
	}
}

type testError string

func (e testError) Error() string { return string(e) }
