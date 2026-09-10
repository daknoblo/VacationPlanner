package foundry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	mainResource  = "/subscriptions/11111111-1111-1111-1111-111111111111/resourcegroups/main/providers/microsoft.cognitiveservices/accounts/main"
	imageResource = "/subscriptions/22222222-2222-2222-2222-222222222222/resourcegroups/images/providers/microsoft.cognitiveservices/accounts/images"
	mainEndpoint  = "https://main.openai.azure.com/openai/v1"
	imageEndpoint = "https://images.services.ai.azure.com/openai/v1"
)

type memoryCache struct {
	mu       sync.Mutex
	values   map[string]string
	getError bool
	putError bool
}

func (m *memoryCache) GetSettings(context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getError {
		return nil, errors.New("private database error")
	}
	result := make(map[string]string)
	for k, v := range m.values {
		result[k] = v
	}
	return result, nil
}

func (m *memoryCache) PutSetting(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putError {
		return errors.New("private database error")
	}
	m.values[key] = value
	return nil
}

type fakeCredential struct {
	mu     sync.Mutex
	scopes []string
	err    error
}

func (f *fakeCredential) GetToken(_ context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes = append(f.scopes, opts.Scopes...)
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, f.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func testConfig() Config {
	return Config{
		ResourceID: mainResource, TenantID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		ClientID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", ClientSecret: "fake-test-credential",
	}
}

func testClient(t *testing.T, cfg Config) (*Client, *memoryCache, *fakeCredential) {
	t.Helper()
	cache := &memoryCache{values: make(map[string]string)}
	c, err := New(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}
	credential := &fakeCredential{}
	c.credential = credential
	return c, cache, credential
}

func accountJSON(resource, endpoint string) string {
	body, _ := json.Marshal(map[string]any{
		"id": resource, "properties": map[string]any{"endpoint": endpoint},
	})
	return string(body)
}

func deploymentJSON(resource, name, model string) map[string]any {
	return map[string]any{
		"id": resource + "/deployments/" + name, "name": name, "sku": map[string]string{"name": "GlobalStandard"},
		"properties": map[string]any{
			"model":             map[string]string{"name": model, "version": "2024-01-01", "format": "OpenAI"},
			"provisioningState": "Succeeded", "capabilities": map[string]string{"chatCompletion": "true"},
		},
	}
}

func deploymentsJSON(resource, name, model, next string) string {
	body, _ := json.Marshal(map[string]any{"value": []any{deploymentJSON(resource, name, model)}, "nextLink": next})
	return string(body)
}

func setDiscovery(c *Client, handler roundTripFunc) {
	c.discovery.Transport = handler
}

func successDiscovery(t *testing.T, c *Client) {
	t.Helper()
	setDiscovery(c, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "management.azure.com" ||
			req.URL.Query().Get("api-version") != armVersion {
			t.Fatalf("unexpected ARM request: %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer test-token" || req.Header.Get("api-key") != "" {
			t.Fatal("incorrect ARM authorization")
		}
		resource, endpoint, model := mainResource, mainEndpoint, "gpt-4o"
		if strings.HasPrefix(req.URL.Path, imageResource) {
			resource, endpoint, model = imageResource, imageEndpoint, "gpt-image-1"
		}
		if req.URL.Path == resource {
			return response(200, accountJSON(resource, endpoint)), nil
		}
		if req.URL.Path != resource+"/deployments" {
			t.Fatalf("unexpected resource: %s", req.URL)
		}
		return response(200, deploymentsJSON(resource, "production", model, "")), nil
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentCatalogsAndPersistentFailures(t *testing.T) {
	cfg := testConfig()
	cfg.ImageResourceID = imageResource
	c, cache, cred := testClient(t, cfg)
	successDiscovery(t, c)
	snapshots, err := c.Snapshots(context.Background())
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("snapshots: %v %v", snapshots, err)
	}
	if snapshots[0].Role != "main" || snapshots[1].Role != "image" ||
		snapshots[0].Deployments[0].Model != "gpt-4o" ||
		snapshots[1].Deployments[0].Model != "gpt-image-1" ||
		snapshots[1].Deployments[0].ChatSupported {
		t.Fatal("same-named deployments were conflated or image adapter enabled")
	}
	for _, scope := range cred.scopes {
		if scope != armScope {
			t.Fatalf("discovery used unexpected audience %q", scope)
		}
	}
	if _, err := c.ResolveChat(context.Background(), ""); err == nil {
		t.Fatal("automatically selected a deployment")
	}
	target, err := c.ResolveChat(context.Background(), "production")
	if err != nil || target.Model != "gpt-4o" || target.ResourceID != mainResource {
		t.Fatalf("alias resolution: %+v %v", target, err)
	}
	successfulMain := cache.values[cacheKey(mainResource, "catalog")]
	calls := 0
	setDiscovery(c, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path == mainResource {
			return response(403, "private network details and secrets"), nil
		}
		return response(401, "another secret"), nil
	})
	if err := c.Refresh(context.Background()); err == nil || calls != 2 {
		t.Fatalf("both resources must be attempted: calls=%d error=%v", calls, err)
	}
	if cache.values[cacheKey(mainResource, "catalog")] != successfulMain {
		t.Fatal("failed refresh replaced successful catalog")
	}
	restarted, err := New(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err = restarted.Snapshots(context.Background())
	if err != nil || !snapshots[0].Stale || !snapshots[1].Stale ||
		snapshots[0].Error != errDiscoveryAuth || snapshots[1].Error != errDiscoveryAuth {
		t.Fatalf("persistent independent errors: %+v %v", snapshots, err)
	}
	for _, value := range cache.values {
		if strings.Contains(value, "secret") || strings.Contains(value, "test-token") || strings.Contains(value, "private network") {
			t.Fatal("sensitive data persisted")
		}
	}
}

func TestImageFailureDoesNotDisableMain(t *testing.T) {
	cfg := testConfig()
	cfg.ImageResourceID = imageResource
	c, _, _ := testClient(t, cfg)
	successDiscovery(t, c)
	setDiscovery(c, func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.Path, imageResource) {
			return response(404, "private details"), nil
		}
		if req.URL.Path == mainResource {
			return response(200, accountJSON(mainResource, mainEndpoint)), nil
		}
		return response(200, deploymentsJSON(mainResource, "production", "gpt-4.1", "")), nil
	})
	if c.Refresh(context.Background()) == nil {
		t.Fatal("image discovery should fail")
	}
	s, err := c.Snapshots(context.Background())
	if err != nil || s[0].Error != "" || s[0].Stale || s[1].Error == "" {
		t.Fatalf("independent discovery status: %+v %v", s, err)
	}
	target, err := c.ResolveChat(context.Background(), "production")
	if err != nil || target.Model != "gpt-4.1" {
		t.Fatalf("main chat disabled by image failure: %+v %v", target, err)
	}
}

func TestPaginationRejectedBeforeTokenForwarding(t *testing.T) {
	for name, next := range map[string]string{
		"foreign host":      "https://attacker.example/x?api-version=" + armVersion,
		"foreign account":   armOrigin + imageResource + "/deployments?api-version=" + armVersion,
		"foreign operation": armOrigin + mainResource + "/listKeys?api-version=" + armVersion,
		"repeated":          armOrigin + mainResource + "/deployments?api-version=" + armVersion,
		"wrong version":     armOrigin + mainResource + "/deployments?api-version=preview",
		"http":              "http://management.azure.com" + mainResource + "/deployments?api-version=" + armVersion,
		"credentials":       "https://user@management.azure.com" + mainResource + "/deployments?api-version=" + armVersion,
		"fragment":          armOrigin + mainResource + "/deployments?api-version=" + armVersion + "#secret",
		"encoded slash":     armOrigin + mainResource + "%2fdeployments?api-version=" + armVersion,
		"duplicate query":   armOrigin + mainResource + "/deployments?api-version=" + armVersion + "&api-version=" + armVersion,
	} {
		t.Run(name, func(t *testing.T) {
			c, _, cred := testClient(t, testConfig())
			calls := 0
			setDiscovery(c, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path == mainResource {
					return response(200, accountJSON(mainResource, mainEndpoint)), nil
				}
				return response(200, deploymentsJSON(mainResource, "production", "gpt-4o", next)), nil
			})
			if err := c.Refresh(context.Background()); err == nil || calls != 2 || len(cred.scopes) != 2 {
				t.Fatalf("unsafe pagination followed: calls=%d scopes=%d err=%v", calls, len(cred.scopes), err)
			}
		})
	}
}

func TestDiscoveryRejectsIncompleteMetadata(t *testing.T) {
	for name, body := range map[string]string{
		"missing list":    `{}`,
		"null list":       `{"value":null}`,
		"missing fields":  `{"value":[{"name":"production"}]}`,
		"foreign account": deploymentsJSON(imageResource, "production", "gpt-4o", ""),
		"empty model":     deploymentsJSON(mainResource, "production", "", ""),
		"invalid name":    deploymentsJSON(mainResource, "../production", "gpt-4o", ""),
		"trailing JSON":   `{"value":[]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, cache, _ := testClient(t, testConfig())
			setDiscovery(c, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == mainResource {
					return response(200, accountJSON(mainResource, mainEndpoint)), nil
				}
				return response(200, body), nil
			})
			if c.Refresh(context.Background()) == nil || cache.values[cacheKey(mainResource, "catalog")] != "" {
				t.Fatal("invalid metadata cached as successful discovery")
			}
		})
	}
}

func TestDiscoveryLimits(t *testing.T) {
	for _, mode := range []string{"page bytes", "total bytes", "page count", "deployment count", "duplicate deployment"} {
		t.Run(mode, func(t *testing.T) {
			c, _, _ := testClient(t, testConfig())
			calls := 0
			setDiscovery(c, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path == mainResource {
					return response(200, accountJSON(mainResource, mainEndpoint)), nil
				}
				switch mode {
				case "page bytes":
					return response(200, strings.Repeat(" ", maxPageBytes+1)), nil
				case "page count":
					return response(200, fmt.Sprintf(`{"value":[],"nextLink":%q}`,
						armOrigin+mainResource+"/deployments?api-version="+armVersion+"&$skiptoken="+fmt.Sprint(calls))), nil
				case "total bytes":
					body := fmt.Sprintf(`{"value":[],"padding":%q,"nextLink":%q}`, strings.Repeat("x", 3<<20),
						armOrigin+mainResource+"/deployments?api-version="+armVersion+"&$skiptoken="+fmt.Sprint(calls))
					return response(200, body), nil
				case "deployment count":
					items := make([]any, 6000)
					for i := range items {
						items[i] = deploymentJSON(mainResource, fmt.Sprintf("d%d-%d", calls, i), "gpt-4o")
					}
					body, _ := json.Marshal(map[string]any{"value": items,
						"nextLink": armOrigin + mainResource + "/deployments?api-version=" + armVersion + "&$skiptoken=" + fmt.Sprint(calls)})
					return response(200, string(body)), nil
				default:
					body, _ := json.Marshal(map[string]any{"value": []any{
						deploymentJSON(mainResource, "dup", "gpt-4o"), deploymentJSON(mainResource, "dup", "gpt-4o"),
					}})
					return response(200, string(body)), nil
				}
			})
			if err := c.Refresh(context.Background()); err == nil {
				t.Fatal("unbounded discovery accepted")
			}
			if calls > maxPages+1 {
				t.Fatalf("page limit exceeded: %d", calls)
			}
		})
	}
}

func TestDiscoveryRedirectAndBoundedRetry(t *testing.T) {
	for _, status := range []int{302, 307, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c, _, _ := testClient(t, testConfig())
			calls := 0
			setDiscovery(c, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Host != "management.azure.com" {
					t.Fatal("token forwarded to redirect target")
				}
				resp := response(status, "private response")
				resp.Header.Set("Location", "https://attacker.example/")
				resp.Header.Set("Retry-After", "0")
				return resp, nil
			})
			if c.Refresh(context.Background()) == nil {
				t.Fatal("request should fail")
			}
			expected := 1
			if retryable(status) {
				expected = 3
			}
			if calls != expected {
				t.Fatalf("attempts = %d, expected %d", calls, expected)
			}
		})
	}
}

func TestChatAuthorizationAndBinding(t *testing.T) {
	for _, version := range []string{"", "2024-10-21"} {
		t.Run(version, func(t *testing.T) {
			cfg := testConfig()
			cfg.APIVersion = version
			c, _, cred := testClient(t, cfg)
			successDiscovery(t, c)
			target, err := c.ResolveChat(context.Background(), "production")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			c.inference.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer test-token" ||
					req.Header.Get("api-key") != "" || req.URL.Host != "main.openai.azure.com" {
					t.Fatal("unsafe inference authentication or resource")
				}
				wantPath := "/openai/v1/chat/completions"
				if version != "" {
					wantPath = "/openai/deployments/production/chat/completions"
				}
				if req.URL.Path != wantPath || req.URL.Query().Get("api-version") != version {
					t.Fatalf("incorrect schema routing: %s", req.URL)
				}
				return response(200, `{"choices":[{"message":{"content":"hello"}}]}`), nil
			})
			payload := []byte(`{"model":"production","messages":[{"role":"user","content":"Hello"}],"max_completion_tokens":8}`)
			if _, err := c.DoChat(context.Background(), target, payload); err != nil || calls != 1 {
				t.Fatalf("chat failed: calls=%d error=%v", calls, err)
			}
			if cred.scopes[len(cred.scopes)-1] != inferenceScope {
				t.Fatal("inference used ARM audience")
			}
			for _, change := range []func(*Target){
				func(v *Target) { v.Endpoint = "https://attacker.example/openai/v1" },
				func(v *Target) { v.ResourceID = imageResource },
				func(v *Target) { v.Deployment = "other" },
				func(v *Target) { v.Model = "gpt-image-1" },
				func(v *Target) { v.ModelVersion = "changed-version" },
				func(v *Target) { v.SupportsTemperature = !v.SupportsTemperature },
				func(v *Target) { v.APIVersion = "preview" },
			} {
				tampered := target
				change(&tampered)
				before := len(cred.scopes)
				if _, err := c.DoChat(context.Background(), tampered, payload); err == nil || len(cred.scopes) != before {
					t.Fatal("tampered target authorized")
				}
			}
		})
	}
}

func TestChatNoRedirectNoRetryAndSafeErrors(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308, 400, 401, 403, 404, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c, _, _ := testClient(t, testConfig())
			successDiscovery(t, c)
			target, err := c.ResolveChat(context.Background(), "production")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			c.inference.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Host != "main.openai.azure.com" {
					t.Fatal("credential forwarded to redirect")
				}
				resp := response(status, `{"error":{"message":"sensitive-private-details"}}`)
				resp.Header.Set("Location", "https://attacker.example/")
				resp.Header.Set("Retry-After", "0")
				return resp, nil
			})
			_, err = c.DoChat(context.Background(), target, []byte(`{"model":"production","messages":[{"role":"user","content":"Hello"}]}`))
			if err == nil || strings.Contains(err.Error(), "sensitive") || calls != 1 {
				t.Fatalf("unsafe error or replay: calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestChatPayloadAndResponseBounds(t *testing.T) {
	c, _, cred := testClient(t, testConfig())
	successDiscovery(t, c)
	target, err := c.ResolveChat(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{}`, `null`, `{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}`,
		`{"model":"production","messages":[{"role":"user","content":[{"type":"image_url","image_url":"https://example.com"}]}]}`,
		`{"model":"production","messages":[{"role":"tool","content":"Hello"}]}`,
		`{"model":"production","messages":[{"role":"user","content":"Hello"}],"tools":[]}`,
		`{"model":"production","messages":[{"role":"user","content":"Hello"}],"stream":true}`,
		`{"model":"production","messages":[{"role":"user","content":"Hello"}],"temperature":3}`,
		strings.Repeat("x", maxChatBytes+1),
	} {
		before := len(cred.scopes)
		if _, err := c.DoChat(context.Background(), target, []byte(payload)); err == nil || before != len(cred.scopes) {
			t.Fatal("invalid payload reached token acquisition")
		}
	}
	c.inference.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, strings.Repeat("x", maxChatBytes+1)), nil
	})
	if _, err := c.DoChat(context.Background(), target,
		[]byte(`{"model":"production","messages":[{"role":"user","content":"Hello"}]}`)); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestCacheErrorsAreSanitizedAndCatalogBound(t *testing.T) {
	c, cache, _ := testClient(t, testConfig())
	successDiscovery(t, c)
	cache.values[cacheKey(mainResource, "status")] = `{"failed":true,"error":"private secret"}`
	s, err := c.Snapshots(context.Background())
	if err != nil || strings.Contains(s[0].Error, "private") || !s[0].Stale {
		t.Fatalf("unsafe cached failure: %v %v", s, err)
	}
	cache.values[cacheKey(mainResource, "catalog")] = strings.ReplaceAll(
		cache.values[cacheKey(mainResource, "catalog")], mainResource, imageResource)
	if _, err := c.ResolveChat(context.Background(), "production"); err == nil {
		t.Fatal("cross-account cached catalog reused")
	}
	cache.getError = true
	if _, err := c.Snapshots(context.Background()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("raw persistence error escaped")
	}
}

func TestEnvironmentSelectionAndAllowList(t *testing.T) {
	cfg := testConfig()
	cfg.Deployment = "production"
	cfg.Models = []string{"gpt-4o"}
	c, _, _ := testClient(t, cfg)
	successDiscovery(t, c)
	target, err := c.ResolveChat(context.Background(), "ignored-saved-selection")
	if err != nil || target.Deployment != "production" {
		t.Fatal("environment selection did not win")
	}
	c.cfg.Models = []string{"production"}
	if _, err := c.ResolveChat(context.Background(), "production"); err == nil {
		t.Fatal("deployment alias bypassed canonical model allow-list")
	}
	c.cfg.Models = []string{"imaginary-model"}
	if _, err := c.ResolveChat(context.Background(), "imaginary-model"); err == nil {
		t.Fatal("allow-list invented a deployment")
	}
}

func TestCredentialFailureNeverFallsBack(t *testing.T) {
	c, _, cred := testClient(t, testConfig())
	successDiscovery(t, c)
	target, err := c.ResolveChat(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	cred.err = errors.New("sensitive credential error")
	c.inference.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("inference attempted after credential failure")
		return nil, errors.New("unexpected transport")
	})
	_, err = c.DoChat(context.Background(), target, []byte(`{"model":"production","messages":[{"role":"user","content":"Hello"}]}`))
	if err == nil || strings.Contains(err.Error(), "sensitive") || !slices.Contains(cred.scopes, inferenceScope) {
		t.Fatalf("authentication failure not safely reported: %v", err)
	}
}

func TestMainFailureStillDiscoversNewImageCatalog(t *testing.T) {
	cfg := testConfig()
	cfg.ImageResourceID = imageResource
	c, _, _ := testClient(t, cfg)
	setDiscovery(c, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case mainResource:
			return response(403, "private details"), nil
		case imageResource:
			return response(200, accountJSON(imageResource, imageEndpoint)), nil
		default:
			return response(200, deploymentsJSON(imageResource, "production", "gpt-image-1", "")), nil
		}
	})
	if c.Refresh(context.Background()) == nil {
		t.Fatal("main failure should be reported")
	}
	s, err := c.Snapshots(context.Background())
	if err != nil || s[0].Endpoint != "" || s[0].Error == "" ||
		s[1].Endpoint != imageEndpoint || s[1].Error != "" || s[1].UpdatedAt.IsZero() {
		t.Fatalf("main failure prevented image discovery: %+v %v", s, err)
	}
	if _, err := c.ResolveChat(context.Background(), "production"); err == nil {
		t.Fatal("chat fell back to the image resource")
	}
}

func TestAccountIdentityRequired(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"id":"foreign","properties":{"endpoint":"https://main.openai.azure.com"}}`,
		`{"properties":{"endpoint":"https://main.openai.azure.com"}}`,
		accountJSON(mainResource, ""),
	} {
		c, _, cred := testClient(t, testConfig())
		setDiscovery(c, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		if err := c.Refresh(context.Background()); err == nil || len(cred.scopes) != 1 {
			t.Fatalf("incomplete account followed by deployment discovery: %v", err)
		}
	}
}

func TestCacheWriteFailurePreservesPreviousCatalog(t *testing.T) {
	c, cache, _ := testClient(t, testConfig())
	successDiscovery(t, c)
	previous := cache.values[cacheKey(mainResource, "catalog")]
	cache.putError = true
	if c.Refresh(context.Background()) == nil {
		t.Fatal("cache failure ignored")
	}
	s, err := c.Snapshots(context.Background())
	if err != nil || s[0].Error != errCacheWrite || !s[0].Stale ||
		cache.values[cacheKey(mainResource, "catalog")] != previous {
		t.Fatalf("cache failure lost prior catalog/status: %+v %v", s, err)
	}
}

func TestRetryAfterAndRefreshCancellation(t *testing.T) {
	if _, retry := retryDelay("120", 0); retry {
		t.Fatal("long Retry-After should abort instead of retrying too early")
	}
	if _, retry := retryDelay("invalid", 0); retry {
		t.Fatal("invalid Retry-After accepted")
	}
	if delay, retry := retryDelay("3", 0); !retry || delay != 3*time.Second {
		t.Fatal("Retry-After ignored")
	}
	c, _, _ := testClient(t, testConfig())
	c.refreshGate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Refresh(ctx); err == nil {
		t.Fatal("queued refresh ignored cancellation")
	}
	<-c.refreshGate
}

func TestReasoningDeploymentRejectsTemperature(t *testing.T) {
	c, cache, cred := testClient(t, testConfig())
	successDiscovery(t, c)
	cache.values[cacheKey(mainResource, "catalog")] = strings.ReplaceAll(
		cache.values[cacheKey(mainResource, "catalog")], "gpt-4o", "gpt-5")
	target, err := c.ResolveChat(context.Background(), "production")
	if err != nil || target.SupportsTemperature {
		t.Fatalf("reasoning parameter support incorrect: %+v %v", target, err)
	}
	before := len(cred.scopes)
	_, err = c.DoChat(context.Background(), target,
		[]byte(`{"model":"production","messages":[{"role":"user","content":"Hello"}],"temperature":0.5}`))
	if err == nil || len(cred.scopes) != before {
		t.Fatal("unsupported temperature reached inference")
	}
}
