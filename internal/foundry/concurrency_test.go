package foundry

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestChatConcurrencyBoundAndSlotRelease(t *testing.T) {
	c, _, credential := testClient(t, testConfig())
	successDiscovery(t, c)
	target, err := c.ResolveChat(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	c.inference.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return response(200, `{"choices":[{"message":{"content":"OK"}}]}`), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := []byte(`{"model":"production","messages":[{"role":"user","content":"Hello"}]}`)
	done := make(chan error, 4)
	for range 4 {
		go func() {
			_, err := c.DoChat(ctx, target, payload)
			done <- err
		}()
	}
	for range 4 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("four requests did not reach inference")
		}
	}
	tokensBefore := len(credential.scopes)
	_, err = c.DoChat(ctx, target, payload)
	if err == nil || !strings.Contains(err.Error(), "busy") || len(credential.scopes) != tokensBefore {
		t.Fatalf("fifth request must fail before token acquisition: %v", err)
	}
	close(release)
	for range 4 {
		if err := <-done; err != nil {
			t.Fatalf("in-flight request failed: %v", err)
		}
	}
	if len(c.chatGate) != 0 {
		t.Fatal("completed requests leaked concurrency slots")
	}
	if _, err := c.DoChat(ctx, target, payload); err != nil {
		t.Fatalf("released slot was not reusable: %v", err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	tokensBefore = len(credential.scopes)
	if _, err := c.DoChat(canceled, target, payload); err == nil ||
		len(credential.scopes) != tokensBefore || len(c.chatGate) != 0 {
		t.Fatal("canceled request obtained a token or leaked a slot")
	}
}

func TestInvalidCacheNotHiddenByDiscoveryFailure(t *testing.T) {
	c, cache, _ := testClient(t, testConfig())
	successDiscovery(t, c)
	cache.values[cacheKey(mainResource, "catalog")] = `{"broken":true}`
	cache.values[cacheKey(mainResource, "status")] = `{"failed":true,"error":"` + errDiscoveryAuth + `"}`
	snapshots, err := c.Snapshots(context.Background())
	if err != nil || len(snapshots) != 1 || !strings.Contains(snapshots[0].Error, "cached catalog is invalid") {
		t.Fatalf("cache corruption masked by discovery failure: %+v %v", snapshots, err)
	}
}
