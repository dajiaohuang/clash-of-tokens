package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthGrant lives only inside the encrypted vault. Metadata never includes
// refresh tokens, client secrets or access-token values.
type OAuthGrant struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenURL     string    `json:"token_url,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
}
type OAuthMetadata struct {
	ExpiresAt        time.Time `json:"expires_at"`
	Scope            string    `json:"scope,omitempty"`
	AccountID        string    `json:"account_id,omitempty"`
	AutomaticRefresh bool      `json:"automatic_refresh"`
}
type refreshFlight struct {
	done chan struct{}
	err  error
}

func validTokenURL(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || (u.Scheme == "http" && ip != nil && ip.IsLoopback())
}
func (grant OAuthGrant) validate() error {
	if grant.AccessToken == "" || len(grant.AccessToken) > 64<<10 || len(grant.RefreshToken) > 64<<10 || len(grant.ClientSecret) > 64<<10 || len(grant.ClientID) > 1024 || len(grant.Scope) > 2048 || len(grant.AccountID) > 256 || strings.ContainsAny(grant.AccessToken, "\r\n\x00") {
		return errors.New("invalid OAuth grant")
	}
	if grant.TokenURL != "" && !validTokenURL(grant.TokenURL) {
		return errors.New("OAuth token endpoint must use HTTPS outside explicit loopback")
	}
	if grant.RefreshToken != "" && (grant.TokenURL == "" || grant.ClientID == "") {
		return errors.New("refresh token requires an explicit token endpoint and client ID")
	}
	return nil
}
func (s *Store) PutOAuth(id, source string, grant OAuthGrant) error {
	if err := grant.validate(); err != nil {
		return err
	}
	return s.putRecord(id, "oauth", source, grant.AccessToken, nil, &grant)
}
func (s *Store) RefreshOAuth(ctx context.Context, id string) error {
	return s.refreshOAuth(ctx, id, true)
}
func (s *Store) refreshOAuth(ctx context.Context, id string, force bool) error {
	s.mu.Lock()
	record, ok := s.records[id]
	if !ok || record.Revoked || record.OAuth == nil {
		s.mu.Unlock()
		return errors.New("credential has no OAuth lifecycle")
	}
	if !force && !record.OAuth.ExpiresAt.IsZero() && time.Now().Add(30*time.Second).Before(record.OAuth.ExpiresAt) {
		s.mu.Unlock()
		return nil
	}
	if s.refreshes == nil {
		s.refreshes = map[string]*refreshFlight{}
	}
	if existing := s.refreshes[id]; existing != nil {
		s.mu.Unlock()
		select {
		case <-existing.done:
			return existing.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if until := s.refreshBackoff[id]; time.Now().Before(until) {
		s.mu.Unlock()
		return errors.New("OAuth reauthorization required or refresh is cooling down")
	}
	grant := *record.OAuth
	if grant.RefreshToken == "" || grant.TokenURL == "" || grant.ClientID == "" {
		s.mu.Unlock()
		return errors.New("OAuth requires reauthorization")
	}
	flight := &refreshFlight{done: make(chan struct{})}
	s.refreshes[id] = flight
	s.mu.Unlock()
	next, err := refreshGrant(ctx, grant)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { delete(s.refreshes, id); close(flight.done) }()
	current, exists := s.records[id]
	if !exists || current.Revoked || current.Version != record.Version {
		flight.err = errors.New("OAuth refresh result is stale")
		return flight.err
	}
	if err != nil {
		if s.refreshBackoff == nil {
			s.refreshBackoff = map[string]time.Time{}
		}
		s.refreshBackoff[id] = time.Now().Add(time.Minute)
		flight.err = err
		return err
	}
	updated := s.copy()
	current.Value = next.AccessToken
	current.OAuth = &next
	current.Version++
	if current.Version == 0 {
		flight.err = errors.New("credential version exhausted")
		return flight.err
	}
	current.UpdatedAt = time.Now().UTC()
	current.OAuthInfo = &OAuthMetadata{ExpiresAt: next.ExpiresAt, Scope: next.Scope, AccountID: next.AccountID, AutomaticRefresh: next.RefreshToken != ""}
	updated[id] = current
	flight.err = s.save(updated)
	delete(s.lastUsed, id)
	delete(s.refreshBackoff, id)
	return flight.err
}
func refreshGrant(ctx context.Context, grant OAuthGrant) (OAuthGrant, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {grant.RefreshToken}, "client_id": {grant.ClientID}}
	if grant.ClientSecret != "" {
		form.Set("client_secret", grant.ClientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, "POST", grant.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return grant, errors.New("invalid OAuth refresh request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 15 * time.Second, MaxResponseHeaderBytes: 64 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return grant, errors.New("OAuth refresh unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || response.StatusCode != 200 {
		return grant, errors.New("OAuth refresh rejected; reauthorize the account")
	}
	defer clear(data)
	var wire struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if json.Unmarshal(data, &wire) != nil || wire.AccessToken == "" || wire.ExpiresIn <= 0 || wire.ExpiresIn > 365*24*3600 || (wire.TokenType != "" && !strings.EqualFold(wire.TokenType, "Bearer")) {
		return grant, errors.New("invalid OAuth refresh response")
	}
	grant.AccessToken = wire.AccessToken
	if wire.RefreshToken != "" {
		grant.RefreshToken = wire.RefreshToken
	}
	if wire.Scope != "" {
		grant.Scope = wire.Scope
	}
	grant.ExpiresAt = time.Now().UTC().Add(time.Duration(wire.ExpiresIn) * time.Second)
	if err = grant.validate(); err != nil {
		return grant, err
	}
	return grant, nil
}
