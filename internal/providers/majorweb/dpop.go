package majorweb

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// consoleDPoPJWK is the public key representation required by console.x.ai's
// DPoP token exchange. It intentionally contains no private key material.
type consoleDPoPJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type consoleDPoPSession struct {
	accessToken string
	privateKey  *ecdsa.PrivateKey
	publicJWK   consoleDPoPJWK
}

func (c *Client) consoleDPoPRequest(ctx context.Context, base, endpoint string, body []byte, stream bool, credential credentials) (*http.Response, error) {
	_ = stream
	for attempt := 0; attempt < 2; attempt++ {
		session, err := c.mintConsoleDPoP(ctx, base, credential.cookie)
		if err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("major web adapter: invalid Console request")
		}
		request.ContentLength = int64(len(body))
		request.GetBody = nil
		request.Header.Set("Accept", "*/*")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Cookie", consoleCookieHeader(credential.cookie))
		request.Header.Set("Authorization", "DPoP "+session.accessToken)
		request.Header.Set("DPoP", consoleDPoPProof(request, session))
		request.Header.Set("Priority", "u=1, i")
		request.Header.Set("User-Agent", "clash-tokens/majorweb")
		request.Header.Set("x-cluster", "https://us-east-1.api.x.ai")
		response, err := c.http.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("major web adapter: Console transport failed")
		}
		if response.StatusCode != http.StatusUnauthorized || attempt > 0 {
			return response, nil
		}
		_ = response.Body.Close()
	}
	return nil, errors.New("major web adapter: Console DPoP retry failed")
}

func (c *Client) mintConsoleDPoP(ctx context.Context, base, cookie string) (consoleDPoPSession, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return consoleDPoPSession{}, errors.New("major web adapter: cannot generate Console DPoP key")
	}
	publicJWK := consoleDPoPJWK{
		Kty: "EC", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.X.FillBytes(make([]byte, 32))),
		Y: base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.Y.FillBytes(make([]byte, 32))),
	}
	payload, err := json.Marshal(map[string]any{"jwk": publicJWK})
	if err != nil {
		return consoleDPoPSession{}, errors.New("major web adapter: cannot encode Console DPoP request")
	}
	tokenEndpoint := endpoint(ensureV1Base(base), "/dpop/token")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, bytes.NewReader(payload))
	if err != nil {
		return consoleDPoPSession{}, errors.New("major web adapter: invalid Console DPoP endpoint")
	}
	request.ContentLength = int64(len(payload))
	request.GetBody = nil
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", consoleCookieHeader(cookie))
	request.Header.Set("User-Agent", "clash-tokens/majorweb")
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return consoleDPoPSession{}, ctx.Err()
		}
		return consoleDPoPSession{}, errors.New("major web adapter: Console DPoP token transport failed")
	}
	data, readErr := readBounded(response.Body, 1<<20)
	_ = response.Body.Close()
	if readErr != nil {
		return consoleDPoPSession{}, readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return consoleDPoPSession{}, &HTTPError{Status: response.StatusCode, What: "Console DPoP token exchange failed"}
	}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if json.Unmarshal(data, &token) != nil || strings.TrimSpace(token.AccessToken) == "" || !strings.EqualFold(strings.TrimSpace(token.TokenType), "DPoP") {
		return consoleDPoPSession{}, errors.New("major web adapter: invalid Console DPoP token response")
	}
	if token.ExpiresIn <= 0 || token.ExpiresIn > 3600 {
		return consoleDPoPSession{}, errors.New("major web adapter: invalid Console DPoP token lifetime")
	}
	return consoleDPoPSession{accessToken: strings.TrimSpace(token.AccessToken), privateKey: privateKey, publicJWK: publicJWK}, nil
}

func consoleCookieHeader(value string) string {
	value = strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(strings.TrimSpace(value))
	if value == "" {
		return value
	}
	parts := strings.Split(value, ";")
	hasSSO := false
	var sso string
	for _, part := range parts {
		name, raw, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "sso":
			hasSSO = true
			sso = strings.TrimSpace(raw)
		}
	}
	if !hasSSO && !strings.Contains(value, "=") {
		sso, hasSSO = value, true
	}
	if !hasSSO || sso == "" {
		return value
	}
	others := make([]string, 0, len(parts)+2)
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		name, _, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower != "sso" && lower != "sso-rw" {
			others = append(others, trimmed)
		}
	}
	result := "sso=" + sso + "; sso-rw=" + sso
	if len(others) > 0 {
		result += "; " + strings.Join(others, "; ")
	}
	return result
}

func consoleDPoPProof(request *http.Request, session consoleDPoPSession) string {
	if request == nil || request.URL == nil || session.privateKey == nil {
		return ""
	}
	accessHash := sha256.Sum256([]byte(session.accessToken))
	claims := map[string]any{
		"jti": randomUUID(),
		"htm": strings.ToUpper(request.Method),
		"htu": request.URL.Scheme + "://" + request.URL.Host + request.URL.EscapedPath(),
		"iat": time.Now().UTC().Unix(),
		"ath": base64.RawURLEncoding.EncodeToString(accessHash[:]),
	}
	header := map[string]any{"alg": "ES256", "typ": "dpop+jwt", "jwk": session.publicJWK}
	headerData, _ := json.Marshal(header)
	claimData, _ := json.Marshal(claims)
	encodedHeader := base64.RawURLEncoding.EncodeToString(headerData)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimData)
	signingInput := encodedHeader + "." + encodedClaims
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, session.privateKey, digest[:])
	if err != nil {
		return ""
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}
