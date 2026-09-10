package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"clash-of-tokens/internal/protocol"
	"clash-of-tokens/internal/providerdef"
	"clash-of-tokens/internal/routing"
	"clash-of-tokens/internal/upstream"
)

type Server struct {
	*metrics
	vault            *credentials.Store
	cfg              config.Config
	Router           *routing.Router
	clients          []clientSlot
	apiKey, adminKey [32]byte
	ingress          chan struct{}
	buffers          sync.Pool
}
type metrics struct {
	buffered                                      atomic.Int64
	requests, rejected, outputBytes, streamErrors atomic.Uint64
}
type clientSlot struct {
	once   sync.Once
	client *upstream.Client
}

func (s *Server) client(i int) *upstream.Client {
	slot := &s.clients[i]
	slot.once.Do(func() {
		slot.client = upstream.NewConfigured(s.cfg.Sources[i], s.cfg.SourceBrowser(s.cfg.Sources[i]), s.cfg.Device)
	})
	return slot.client
}
func New(c config.Config) (*Server, error) {
	return NewWithKeys(c, os.Getenv(c.APIKeyEnv), os.Getenv(c.AdminKeyEnv))
}
func NewWithKeys(c config.Config, key, admin string) (*Server, error) {
	return NewWithCredentials(c, key, admin, nil)
}

func NewWithCredentials(c config.Config, key, admin string, resolver func(string) string) (*Server, error) {
	c.Sources = append([]config.Source(nil), c.Sources...)
	for i := range c.Sources {
		c.Sources[i].CredentialRef = c.SourceCredentialRef(c.Sources[i])
		c.Sources[i].CredentialResolver = resolver
	}
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if len(key) < 16 || len(admin) < 16 || key == admin {
		return nil, errors.New("distinct API and admin secrets of at least 16 characters must be set")
	}
	s := &Server{metrics: &metrics{}, cfg: c, Router: routing.New(c), apiKey: sha256.Sum256([]byte(key)), adminKey: sha256.Sum256([]byte(admin)), ingress: make(chan struct{}, c.Runtime.MaxInflight+c.Runtime.MaxQueued), clients: make([]clientSlot, len(c.Sources))}
	s.buffers.New = func() any { b := make([]byte, 32<<10); return &b }
	return s, nil
}
func (s *Server) Close() {
	for i := range s.clients {
		slot := &s.clients[i]
		slot.once.Do(func() {})
		if slot.client != nil {
			slot.client.Close()
		}
	}
}
func authorized(r *http.Request, key [32]byte) bool {
	v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if v == "" {
		v = r.Header.Get("x-api-key")
	}
	if v == "" {
		v = r.Header.Get("x-goog-api-key")
	}
	h := sha256.Sum256([]byte(v))
	return subtle.ConstantTimeCompare(h[:], key[:]) == 1
}
func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg, "type": "gateway_error"}})
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" && (r.URL.Path == "/assets/control.js" || r.URL.Path == "/assets/control.css") {
		if r.URL.Path == "/assets/control.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = io.WriteString(w, controlJS)
		} else {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = io.WriteString(w, controlCSS)
		}
		return
	}
	if (r.URL.Path == "/" || r.URL.Path == "/chat") && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
		page := dashboard
		if r.URL.Path == "/chat" {
			page = chatPage
		}
		_, _ = io.WriteString(w, page)
		return
	}
	admin := strings.HasPrefix(r.URL.Path, "/admin/")
	key := s.apiKey
	if admin {
		key = s.adminKey
	}
	if !authorized(r, key) {
		fail(w, 401, "authentication required")
		return
	}
	if admin {
		s.admin(w, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/healthz" {
		reply(w, map[string]string{"status": "ok"})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/models" {
		s.models(w)
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return
	}
	proto, model, stream := endpoint(r.URL.Path)
	if proto == "" {
		fail(w, 404, "unsupported endpoint")
		return
	}
	if r.URL.RawQuery != "" && r.URL.RawQuery != "alt=sse" {
		fail(w, 400, "unsupported query parameters")
		return
	}
	s.generate(w, r, proto, model, stream)
}
func endpoint(path string) (string, string, bool) {
	switch path {
	case "/v1/chat/completions":
		return "chat", "", false
	case "/v1/responses":
		return "responses", "", false
	case "/v1/messages":
		return "messages", "", false
	}
	if strings.HasPrefix(path, "/v1beta/models/") {
		v := strings.TrimPrefix(path, "/v1beta/models/")
		for _, a := range []string{":generateContent", ":streamGenerateContent"} {
			if strings.HasSuffix(v, a) {
				return "gemini", strings.TrimSuffix(v, a), a == ":streamGenerateContent"
			}
		}
	}
	return "", "", false
}
func (s *Server) reserve(n int64) bool {
	for {
		v := s.buffered.Load()
		if n > s.cfg.Runtime.MaxBufferedBytes-v {
			return false
		}
		if s.buffered.CompareAndSwap(v, v+n) {
			return true
		}
	}
}
func (s *Server) generate(w http.ResponseWriter, r *http.Request, proto, pathModel string, pathStream bool) {
	s.requests.Add(1)
	select {
	case s.ingress <- struct{}{}:
		defer func() { <-s.ingress }()
	default:
		s.rejected.Add(1)
		fail(w, 503, "ingress capacity exhausted")
		return
	}
	// Reserve both the input and a potential rewritten copy before reading any
	// body. Chunked bodies reserve the maximum, never unbounded io.ReadAll growth.
	size := r.ContentLength
	if size < 0 {
		size = s.cfg.Runtime.MaxBodyBytes
	}
	if size > s.cfg.Runtime.MaxBodyBytes {
		fail(w, 413, "request body too large")
		return
	}
	reservation := 2*(size+1) + 8192
	if !s.reserve(reservation) {
		s.rejected.Add(1)
		fail(w, 503, "request byte budget exhausted")
		return
	}
	defer func() { s.buffered.Add(-reservation) }()
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(time.Duration(s.cfg.Runtime.BodyReadTimeoutMS) * time.Millisecond))
	defer rc.SetReadDeadline(time.Time{})
	body, e := readIngressBody(r.Body, size, r.ContentLength < 0)
	n := len(body)
	if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
		fail(w, 400, "cannot read request")
		return
	}
	if int64(n) > size {
		fail(w, 413, "request body exceeds declared or configured limit")
		return
	}
	body = body[:n]
	// Upload admission reserves the worst case. Once the upload has ended,
	// retain only the actual backing capacity plus a possible rewritten copy.
	// This matters when small chunked requests wait behind long generations.
	retained := int64(cap(body)) + int64(len(body)) + 8192
	if retained < reservation {
		s.buffered.Add(retained - reservation)
		reservation = retained
	}
	_ = rc.SetReadDeadline(time.Time{})
	meta, e := protocol.Inspect(body)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if proto == "gemini" {
		if meta.Model != "" {
			fail(w, 400, "Gemini model must be in URL only")
			return
		}
		meta.Model = pathModel
		meta.Stream = pathStream
	}
	if meta.Model == "" {
		fail(w, 400, "model required")
		return
	}
	if r.Header.Get("X-COT-Session") != "" {
		meta.Stateful = true
	}
	affinity := r.Header.Get("X-COT-Affinity")
	if meta.Stream {
		streamBudget := 4*min(s.cfg.Runtime.MaxOutputBytes, 1<<20) + 8192
		if !s.reserve(streamBudget) {
			s.rejected.Add(1)
			fail(w, 503, "stream memory budget exhausted")
			return
		}
		defer s.buffered.Add(-streamBudget)
	}
	if len(affinity) > 128 {
		fail(w, 400, "affinity identifier exceeds 128 bytes")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.Runtime.RequestTimeoutMS)*time.Millisecond)
	defer cancel()
	lease, resp, e := s.acquireResponse(ctx, routing.Query{Model: meta.Model, Protocol: proto, Tools: meta.Tools, Bytes: int64(n), Stateful: meta.Stateful, Vision: meta.Vision, Affinity: affinity}, meta.Stream, r.Header, func(model string) []byte { return protocol.Rewrite(body, meta, model) })
	if lease == nil {
		s.rejected.Add(1)
		fail(w, 503, e.Error())
		return
	}
	status := 0
	var cooldown time.Duration
	defer func() { lease.Release(status, cooldown) }()
	// Client.Do closes the payload reader before returning, including on an
	// early response. Long generations no longer retain large prompt buffers.
	body = nil
	if s.cfg.Sources[lease.Target.Source].Adapter != "chatgpt-web" {
		s.buffered.Add(-reservation)
		reservation = 0
	}
	if e != nil {
		clientStatus := 502
		var classified interface{ HTTPStatus() int }
		if errors.As(e, &classified) {
			clientStatus = classified.HTTPStatus()
			status = clientStatus
		}
		if ctx.Err() != nil {
			status = 499
		}
		fail(w, clientStatus, e.Error())
		return
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	cooldown = retryAfter(resp.Header.Get("Retry-After"))
	if status < 200 || status >= 300 {
		if status == 429 {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(cooldown.Seconds()))))
		}
		fail(w, statusToClient(status), "upstream returned status "+strconv.Itoa(status))
		return
	}
	ct := resp.Header.Get("Content-Type")
	if meta.Stream && !strings.HasPrefix(ct, "text/event-stream") {
		status = 502
		fail(w, 502, "upstream did not return an SSE stream")
		return
	}
	for _, key := range []string{"X-COT-Session", "X-COT-Response-Id", "X-COT-Delivery", "X-COT-Extraction", "X-COT-Context", "X-COT-Completion"} {
		if v := resp.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-COT-Source", s.cfg.Sources[lease.Target.Source].ID)
	w.Header().Set("X-Accel-Buffering", "no")
	if meta.Stream {
		result, sent := s.streamResponse(r.Context(), w, resp.Body, proto, lease)
		result.UpstreamStatus = resp.StatusCode
		lease.RecordExecution(result)
		if !result.Successful() {
			if result.ClientCanceled {
				status = 499
				return
			}
			status = 502
			s.streamErrors.Add(1)
			if sent {
				panic(http.ErrAbortHandler)
			}
			fail(w, 502, "upstream stream did not complete")
		}
		return
	}
	w.WriteHeader(status)
	buf := s.buffers.Get().(*[]byte)
	defer s.buffers.Put(buf)
	var total int64
	for {
		n, readErr := resp.Body.Read(*buf)
		if n > 0 {
			total += int64(n)
			if total > s.cfg.Runtime.MaxOutputBytes {
				status = 502
				s.streamErrors.Add(1)
				panic(http.ErrAbortHandler)
			}
			_ = rc.SetWriteDeadline(time.Now().Add(time.Duration(s.cfg.Runtime.WriteTimeoutMS) * time.Millisecond))
			written, writeErr := w.Write((*buf)[:n])
			s.outputBytes.Add(uint64(written))
			if writeErr != nil {
				status = 499
				return
			}
			if meta.Stream {
				if e = rc.Flush(); e != nil {
					status = 499
					return
				}
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				status = 502
				s.streamErrors.Add(1)
				panic(http.ErrAbortHandler)
			}
			break
		}
	}
	_ = rc.SetWriteDeadline(time.Time{})
}

func readIngressBody(r io.Reader, size int64, chunked bool) ([]byte, error) {
	if chunked {
		// Admission still reserves the maximum. Allocate only received bytes;
		// the extra byte retains oversized-body detection without an unbounded read.
		limit := size + 1
		body := make([]byte, 0, min(int64(4096), limit))
		emptyReads := 0
		for int64(len(body)) < limit {
			if len(body) == cap(body) {
				next := make([]byte, len(body), min(int64(cap(body))*2, limit))
				copy(next, body)
				body = next
			}
			n, err := r.Read(body[len(body):cap(body)])
			body = body[:len(body)+n]
			if err == io.EOF {
				return body, nil
			}
			if err != nil {
				return body, err
			}
			if n == 0 {
				emptyReads++
				if emptyReads == 100 {
					return body, io.ErrNoProgress
				}
			} else {
				emptyReads = 0
			}
		}
		return body, nil
	}
	body := make([]byte, size+1)
	n, err := io.ReadFull(io.LimitReader(r, size+1), body)
	return body[:n], err
}

func statusToClient(n int) int {
	if n >= 400 && n <= 599 {
		return n
	}
	return 502
}
func retryAfter(v string) time.Duration {
	if n, e := strconv.Atoi(v); e == nil {
		return time.Duration(max(0, min(n, 86400))) * time.Second
	}
	if t, e := http.ParseTime(v); e == nil {
		return max(0, min(time.Until(t), 24*time.Hour))
	}
	return 30 * time.Second
}
func (s *Server) models(w http.ResponseWriter) {
	type modelEntry struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	states := s.Router.Status()
	count := len(s.cfg.Groups)
	for i, src := range s.cfg.Sources {
		if states[i].Enabled {
			count += len(src.Models)
		}
	}
	out := make([]modelEntry, 0, count)
	for i, src := range s.cfg.Sources {
		if !states[i].Enabled {
			continue
		}
		for _, m := range src.Models {
			if m.Enabled != nil && !*m.Enabled {
				continue
			}
			out = append(out, modelEntry{src.ID + "/" + m.ID, "model", src.Provider})
		}
	}
	for _, g := range s.cfg.Groups {
		out = append(out, modelEntry{g.ID, "model", "clash-tokens"})
	}
	reply(w, struct {
		Object string       `json:"object"`
		Data   []modelEntry `json:"data"`
	}{"list", out})
}
func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != scheme+"://"+r.Host {
			fail(w, 403, "cross-origin mutation rejected")
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/admin/credentials") {
		s.credentialAdmin(w, r)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/admin/preset":
		source, err := catalog.Preset(r.URL.Query().Get("provider"), r.URL.Query().Get("model"), r.URL.Query().Get("base_url"))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		reply(w, source)
	case r.Method == "GET" && r.URL.Path == "/admin/descriptors":
		reply(w, providerdef.All())
	case r.Method == "GET" && r.URL.Path == "/admin/catalog":
		reply(w, catalog.All())
	case r.Method == "GET" && r.URL.Path == "/admin/status":
		reply(w, s.runtimeStatus())
	case r.Method == "GET" && r.URL.Path == "/admin/explain":
		reply(w, s.Router.Explain(routing.Query{Model: r.URL.Query().Get("model"), Protocol: r.URL.Query().Get("protocol")}))
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/admin/sources/"):
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != scheme+"://"+r.Host {
			fail(w, 403, "cross-origin mutation rejected")
			return
		}
		var p struct {
			Enabled *bool `json:"enabled"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		d.DisallowUnknownFields()
		if d.Decode(&p) != nil || p.Enabled == nil || d.Decode(new(any)) != io.EOF {
			fail(w, 400, "expected enabled boolean")
			return
		}
		if !s.Router.SetEnabled(strings.TrimPrefix(r.URL.Path, "/admin/sources/"), *p.Enabled) {
			fail(w, 404, "unknown source")
			return
		}
		reply(w, map[string]string{"status": "updated", "persistence": "runtime only; edit config for restart"})
	default:
		fail(w, 404, "unknown admin endpoint")
	}
}
