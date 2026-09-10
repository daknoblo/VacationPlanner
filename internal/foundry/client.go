package foundry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	armVersion        = "2024-10-01"
	armOrigin         = "https://management.azure.com"
	armScope          = "https://management.azure.com/.default"
	inferenceScope    = "https://cognitiveservices.azure.com/.default"
	maxPages          = 100
	maxDeployments    = 10000
	maxPageBytes      = 4 << 20
	maxDiscoveryBytes = 16 << 20
	maxChatBytes      = 1 << 20
)

const (
	errDiscoveryToken     = "foundry discovery authentication failed; check service principal configuration"
	errDiscoveryTransport = "foundry discovery connection failed or timed out"
	errDiscoveryAuth      = "foundry discovery authorization denied; check account-scoped ARM read permissions"
	errDiscoveryHTTP      = "foundry discovery HTTP request failed"
	errDiscoveryLimit     = "foundry discovery exceeded metadata limits"
	errDiscoveryMetadata  = "foundry discovery returned incomplete or inconsistent metadata"
	errDiscoveryPage      = "foundry discovery returned an unsafe or repeated pagination link"
	errDiscoveryEndpoint  = "foundry discovery has no unambiguous supported endpoint; check account metadata and endpoint override"
	errCacheWrite         = "foundry discovery metadata could not be persisted"
)

type Client struct {
	cfg         Config
	credential  azcore.TokenCredential
	cache       Cache
	discovery   *http.Client
	inference   *http.Client
	mu          sync.RWMutex
	refreshGate chan struct{}
	chatGate    chan struct{}
	cacheErrors map[string]string
}

// New constructs one explicit credential without performing network requests.
func New(cfg Config, cache Cache) (*Client, error) {
	if !cfg.Requested() {
		return nil, errors.New("foundry requires explicit service principal configuration")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cache == nil {
		return nil, errors.New("foundry requires a persistent metadata cache")
	}
	credential, err := azidentity.NewClientSecretCredential(cfg.TenantID, cfg.ClientID, cfg.ClientSecret,
		&azidentity.ClientSecretCredentialOptions{
			ClientOptions: azcore.ClientOptions{
				Cloud: cloud.AzurePublic,
				Transport: &http.Client{
					Timeout: 15 * time.Second, CheckRedirect: rejectRedirect,
				},
			},
		})
	if err != nil {
		return nil, errors.New("foundry explicit service principal credential could not be configured")
	}
	cfg.ResourceID, _ = canonicalResourceID(cfg.ResourceID)
	if cfg.ImageResourceID != "" {
		cfg.ImageResourceID, _ = canonicalResourceID(cfg.ImageResourceID)
	}
	cfg.ClientSecret = ""
	cfg.Models = append([]string(nil), cfg.Models...)
	return &Client{
		cfg: cfg, credential: credential, cache: cache,
		discovery:   &http.Client{Timeout: 15 * time.Second, CheckRedirect: rejectRedirect},
		inference:   &http.Client{Timeout: 90 * time.Second, CheckRedirect: rejectRedirect},
		cacheErrors: make(map[string]string),
		refreshGate: make(chan struct{}, 1),
		chatGate:    make(chan struct{}, 4),
	}, nil
}

func rejectRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// Config returns a copy without the client secret.
func (c *Client) Config() Config {
	cfg := c.cfg
	cfg.ClientSecret = ""
	cfg.Models = append([]string(nil), cfg.Models...)
	return cfg
}

func (c *Client) resources() []string {
	resources := []string{c.cfg.ResourceID}
	if c.cfg.ImageResourceID != "" && c.cfg.ImageResourceID != c.cfg.ResourceID {
		resources = append(resources, c.cfg.ImageResourceID)
	}
	return resources
}

// Refresh reads only explicitly configured ARM accounts. It never generates
// content. Each account has its own budget so a main-account failure cannot
// consume all the time allocated to a separate image account.
func (c *Client) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	select {
	case c.refreshGate <- struct{}{}:
		defer func() { <-c.refreshGate }()
	case <-ctx.Done():
		return errors.New(errDiscoveryTransport)
	}
	var failures []error
	for i, resource := range c.resources() {
		accountCtx, accountCancel := context.WithTimeout(ctx, 20*time.Second)
		override := ""
		if i == 0 {
			override = c.cfg.Endpoint
		}
		catalog, err := c.discover(accountCtx, resource, override)
		accountCancel()
		if saveErr := c.persist(ctx, resource, catalog, err); saveErr != nil {
			failures = append(failures, saveErr)
		}
		if err != nil {
			role := "main"
			if i > 0 {
				role = "image"
			}
			failures = append(failures, fmt.Errorf("%s resource: %w", role, err))
		}
	}
	return errors.Join(failures...)
}

func (c *Client) persist(ctx context.Context, resource string, catalog cachedCatalog, refreshErr error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := refreshStatus{At: time.Now().UTC(), Failed: refreshErr != nil}
	persistFailed := false
	if refreshErr != nil {
		status.Error = safeCachedError(refreshErr.Error())
	} else {
		raw, err := json.Marshal(catalog)
		if err != nil || len(raw) > maxDiscoveryBytes ||
			c.cache.PutSetting(ctx, cacheKey(resource, "catalog"), string(raw)) != nil {
			persistFailed = true
			status.Failed, status.Error = true, errCacheWrite
		}
	}
	raw, err := json.Marshal(status)
	if err != nil || c.cache.PutSetting(ctx, cacheKey(resource, "status"), string(raw)) != nil {
		persistFailed = true
	}
	if persistFailed {
		c.cacheErrors[resource] = errCacheWrite
		return errors.New(errCacheWrite)
	}
	delete(c.cacheErrors, resource)
	return nil
}

func safeCachedError(message string) string {
	switch message {
	case errDiscoveryToken, errDiscoveryTransport, errDiscoveryAuth, errDiscoveryHTTP,
		errDiscoveryLimit, errDiscoveryMetadata, errDiscoveryPage, errDiscoveryEndpoint, errCacheWrite:
		return message
	default:
		return "foundry discovery failed; refresh metadata"
	}
}

func (c *Client) discover(ctx context.Context, resource, override string) (cachedCatalog, error) {
	var catalog cachedCatalog
	budget := maxDiscoveryBytes
	body, err := c.armGET(ctx, armOrigin+resource+"?api-version="+armVersion, &budget)
	if err != nil {
		return catalog, err
	}
	var account struct {
		ID         string             `json:"id"`
		Properties *accountProperties `json:"properties"`
	}
	if json.Unmarshal(body, &account) != nil || account.Properties == nil ||
		!strings.EqualFold(account.ID, resource) {
		return catalog, errors.New(errDiscoveryMetadata)
	}
	endpoint, err := selectEndpoint(*account.Properties, override)
	if err != nil {
		return catalog, errors.New(errDiscoveryEndpoint)
	}
	catalog = cachedCatalog{
		Snapshot: Snapshot{ResourceID: resource, Endpoint: endpoint, Deployments: []Deployment{}},
		Account:  *account.Properties,
	}
	next := armOrigin + resource + "/deployments?api-version=" + armVersion
	seen := make(map[string]bool)
	names := make(map[string]bool)
	for page := 0; next != ""; page++ {
		if page >= maxPages {
			return cachedCatalog{}, errors.New(errDiscoveryLimit)
		}
		canonical, err := paginationURL(next, resource)
		if err != nil || seen[canonical] {
			return cachedCatalog{}, errors.New(errDiscoveryPage)
		}
		seen[canonical] = true
		body, err := c.armGET(ctx, canonical, &budget)
		if err != nil {
			return cachedCatalog{}, err
		}
		var response struct {
			Value    *[]armDeployment `json:"value"`
			NextLink string           `json:"nextLink"`
		}
		if json.Unmarshal(body, &response) != nil || response.Value == nil {
			return cachedCatalog{}, errors.New(errDiscoveryMetadata)
		}
		if len(catalog.Deployments)+len(*response.Value) > maxDeployments {
			return cachedCatalog{}, errors.New(errDiscoveryLimit)
		}
		for _, item := range *response.Value {
			d, err := item.deployment(resource)
			name := strings.ToLower(d.Name)
			if err != nil || names[name] {
				return cachedCatalog{}, errors.New(errDiscoveryMetadata)
			}
			names[name] = true
			classify(&d, c.cfg.Models)
			catalog.Deployments = append(catalog.Deployments, d)
		}
		next = response.NextLink
	}
	sort.Slice(catalog.Deployments, func(i, j int) bool { return catalog.Deployments[i].Name < catalog.Deployments[j].Name })
	catalog.UpdatedAt = time.Now().UTC()
	return catalog, nil
}

type armDeployment struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	SKU        map[string]any `json:"sku"`
	Properties *struct {
		Model *struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Format  string `json:"format"`
		} `json:"model"`
		ProvisioningState string            `json:"provisioningState"`
		Capabilities      map[string]string `json:"capabilities"`
	} `json:"properties"`
}

func (item armDeployment) deployment(resource string) (Deployment, error) {
	if item.Properties == nil || item.Properties.Model == nil ||
		!strings.EqualFold(item.ID, resource+"/deployments/"+item.Name) {
		return Deployment{}, errors.New(errDiscoveryMetadata)
	}
	d := Deployment{
		Name: item.Name, ResourceID: resource, SKU: item.SKU,
		Model: item.Properties.Model.Name, ModelVersion: item.Properties.Model.Version,
		ModelFormat: item.Properties.Model.Format, ProvisioningState: item.Properties.ProvisioningState,
		Capabilities: item.Properties.Capabilities,
	}
	if !validDeployment(d, resource) {
		return Deployment{}, errors.New(errDiscoveryMetadata)
	}
	return d, nil
}

func paginationURL(raw, resource string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.Scheme != "https" || u.Host != "management.azure.com" ||
		u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" ||
		!strings.EqualFold(u.Path, resource+"/deployments") {
		return "", errors.New(errDiscoveryPage)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query["api-version"]) != 1 || query.Get("api-version") != armVersion {
		return "", errors.New(errDiscoveryPage)
	}
	for key, values := range query {
		if (key != "api-version" && key != "$skiptoken" && key != "skiptoken" && key != "$skip" && key != "$top") ||
			len(values) != 1 || values[0] == "" {
			return "", errors.New(errDiscoveryPage)
		}
	}
	u.Path = resource + "/deployments"
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (c *Client) armGET(ctx context.Context, endpoint string, budget *int) ([]byte, error) {
	for attempt := 0; attempt < 3; attempt++ {
		token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
		if err != nil || token.Token == "" {
			return nil, errors.New(errDiscoveryToken)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, errors.New(errDiscoveryPage)
		}
		req.Header.Set("Authorization", "Bearer "+token.Token)
		req.Header.Set("Accept", "application/json")
		resp, err := c.discovery.Do(req)
		if err != nil {
			return nil, errors.New(errDiscoveryTransport)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
		_ = resp.Body.Close()
		if len(data) > maxPageBytes || len(data) > *budget {
			return nil, errors.New(errDiscoveryLimit)
		}
		*budget -= len(data)
		if readErr != nil {
			return nil, errors.New(errDiscoveryTransport)
		}
		if resp.StatusCode == http.StatusOK {
			return data, nil
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, errors.New(errDiscoveryAuth)
		}
		if !retryable(resp.StatusCode) || attempt == 2 {
			return nil, errors.New(errDiscoveryHTTP)
		}
		delay, retry := retryDelay(resp.Header.Get("Retry-After"), attempt)
		if !retry {
			return nil, errors.New(errDiscoveryHTTP)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.New(errDiscoveryTransport)
		case <-timer.C:
		}
	}
	return nil, errors.New(errDiscoveryHTTP)
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryDelay(raw string, attempt int) (time.Duration, bool) {
	delay := time.Duration(attempt+1) * 200 * time.Millisecond
	if raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			if seconds < 0 || seconds > 5 {
				return 0, false
			}
			delay = time.Duration(seconds) * time.Second
		} else if at, err := http.ParseTime(raw); err == nil {
			delay = max(time.Until(at), 0)
		} else {
			return 0, false
		}
	}
	return delay, delay <= 5*time.Second
}
