package routing

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
)

var ErrUnavailable = errors.New("no eligible source; check protocol, approval, tier, quota and enabled state")
var ErrQueueFull = errors.New("queue is full")

type Target struct {
	Source int
	Model  config.Model
}
type state struct {
	enabled   bool
	active    int
	cooldown  time.Time
	failures  uint64
	completed uint64
	latency   float64
	blocked   bool
}
type quota struct {
	active, limit int
	cooldown      time.Time
}
type compiledGroup struct {
	config.Group
	members map[string]int
}
type waiter struct {
	query Query
	ctx   context.Context
	next  *waiter
	ready chan struct{}
}
type Router struct {
	*capacity
	cfg            config.Config
	targets        []Target
	groups         map[string]compiledGroup
	candidates     map[string][]int
	state          []*state
	quotas         []*quota
	accounts       []*quota
	parentEnabled  []bool
	parentApproved []bool
	waiting        int
	retired        bool
	cursor         uint64
	head, tail     *waiter
}

// Capacity survives configuration generations, including leases held by
// retired routers. A hot update never doubles an account's available slots.
type capacity struct {
	mu            sync.Mutex
	active        int
	sourceStates  map[string]*state
	quotaStates   map[string]*quota
	accountStates map[string]*quota
	current       *Router
}
type Query struct {
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
	Tools    bool   `json:"tools"`
	Bytes    int64  `json:"bytes"`
	Stateful bool   `json:"stateful"`
	Vision   bool   `json:"vision"`
}
type Lease struct {
	router  *Router
	Target  Target
	once    sync.Once
	started time.Time
	state   *state
	quota   *quota
	account *quota
}

func New(c config.Config) *Router {
	r := &Router{capacity: &capacity{sourceStates: map[string]*state{}, quotaStates: map[string]*quota{}, accountStates: map[string]*quota{}}, cfg: c, groups: make(map[string]compiledGroup), candidates: make(map[string][]int), state: make([]*state, len(c.Sources))}
	r.current = r
	qs := map[string]*quota{}
	accountLimits := map[string]*quota{}
	accountConfig := map[string]config.Account{}
	providerConfig := map[string]config.Provider{}
	for _, p := range c.Providers {
		providerConfig[p.ID] = p
	}
	for _, a := range c.Accounts {
		accountConfig[a.ID] = a
		accountLimits[a.ID] = &quota{limit: a.MaxInflight}
		r.accountStates[a.ID] = accountLimits[a.ID]
	}
	for i, s := range c.Sources {
		enabled, approved := true, true
		if p, ok := providerConfig[s.Provider]; ok {
			enabled, approved = p.Enabled, p.AutoApproved
		}
		if a, ok := accountConfig[s.AccountID]; ok {
			enabled = enabled && a.Enabled
			approved = approved && a.AutoApproved
		}
		r.parentEnabled = append(r.parentEnabled, enabled)
		r.parentApproved = append(r.parentApproved, approved)
		r.accounts = append(r.accounts, accountLimits[s.AccountID])
		r.state[i] = &state{enabled: s.Enabled}
		r.sourceStates[s.ID] = r.state[i]
		if qs[s.QuotaDomain] == nil {
			qs[s.QuotaDomain] = &quota{limit: s.QuotaMaxInflight}
		}
		r.quotas = append(r.quotas, qs[s.QuotaDomain])
		r.quotaStates[s.QuotaDomain] = qs[s.QuotaDomain]
		for _, m := range s.Models {
			r.targets = append(r.targets, Target{i, m})
		}
	}
	for _, g := range c.Groups {
		cg := compiledGroup{Group: g, members: make(map[string]int, len(g.Sources))}
		for i, id := range g.Sources {
			cg.members[id] = i
		}
		r.groups[g.ID] = cg
	}
	for i, t := range r.targets {
		s := c.Sources[t.Source]
		r.candidates[s.ID+"/"+t.Model.ID] = append(r.candidates[s.ID+"/"+t.Model.ID], i)
		r.candidates[t.Model.ID] = append(r.candidates[t.Model.ID], i)
		for id, g := range r.groups {
			if _, ok := g.members[s.ID]; ok {
				r.candidates[id] = append(r.candidates[id], i)
			}
		}
	}
	return r
}

// All callers hold mu. Wake the first runnable waiter, not the whole queue.
// Its removal passes the baton, allowing additional free accounts to fill.
func (r *Router) signal() {
	now := time.Now()
	for w := r.head; w != nil; w = w.next {
		idx, eligible := r.choose(w.query, now)
		if w.ctx.Err() != nil || idx >= 0 || !eligible {
			select {
			case w.ready <- struct{}{}:
			default:
			}
			return
		}
	}
}
func contains(a []string, v string) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
func (r *Router) eligible(t Target, q Query) (bool, string) {
	s := r.cfg.Sources[t.Source]
	st := r.state[t.Source]
	if t.Model.Enabled != nil && !*t.Model.Enabled {
		return false, "model_disabled"
	}
	if !st.enabled {
		return false, "disabled"
	}
	if !r.parentEnabled[t.Source] {
		return false, "provider_or_account_disabled"
	}
	if st.blocked {
		return false, "authentication_or_policy_block"
	}
	if !contains(t.Model.Protocols, q.Protocol) {
		return false, "protocol"
	}
	if q.Bytes > t.Model.MaxInputBytes {
		return false, "input_limit"
	}
	if q.Tools && t.Model.Tools != "native" {
		return false, "native_tools_required"
	}
	if q.Vision && !t.Model.Vision {
		return false, "vision_required"
	}
	groupID := q.Model
	min := -1
	if strings.HasPrefix(q.Model, "auto/") {
		groupID = "auto"
		min = config.Tier(strings.TrimPrefix(q.Model, "auto/"))
		if min < 0 {
			return false, "invalid_tier"
		}
	}
	if g, ok := r.groups[groupID]; ok {
		if t.Model.AutoApproved != nil && !*t.Model.AutoApproved {
			return false, "model_not_approved"
		}
		if q.Stateful {
			return false, "stateful_requires_explicit_source"
		}
		_, member := g.members[s.ID]
		if !s.AutoApproved || !r.parentApproved[t.Source] || !member {
			return false, "not_approved"
		}
		if g.LocalOnly && !s.LocalInference() {
			return false, "local_only"
		}
		if !g.BillingAllowed(s) {
			return false, "paid_not_allowed"
		}
		if len(g.AllowedSourceKinds) > 0 && !contains(g.AllowedSourceKinds, s.SourceKind) {
			return false, "source_kind"
		}
		if g.RequireTools && t.Model.Tools != "native" {
			return false, "native_tools_required"
		}
		if g.Type == "select" && (len(g.Sources) == 0 || g.Sources[0] != s.ID) {
			return false, "not_selected"
		}
		if min < config.Tier(g.MinTier) {
			min = config.Tier(g.MinTier)
		}
		tier := config.Tier(t.Model.Tier)
		if (tier < 0 && !g.AllowUnrated) || (tier >= 0 && tier < min) {
			return false, "quality_floor"
		}
		return true, ""
	}
	if q.Model == s.ID+"/"+t.Model.ID {
		return true, ""
	}
	if !q.Stateful && q.Model == t.Model.ID {
		return true, ""
	}
	return false, "model"
}
func (r *Router) choose(q Query, now time.Time) (int, bool) {
	if r.retired {
		return -1, false
	}
	best := -1
	score := math.Inf(1)
	eligible := false
	key := q.Model
	if strings.HasPrefix(key, "auto/") {
		key = "auto"
	}
	g := r.groups[key]
	candidates := r.candidates[key]
	position := 0
	if len(candidates) > 0 {
		position = int(r.cursor % uint64(len(candidates)))
	}
	for off := 0; off < len(candidates); off++ {
		idx := candidates[position]
		position++
		if position == len(candidates) {
			position = 0
		}
		t := r.targets[idx]
		ok, _ := r.eligible(t, q)
		if !ok {
			continue
		}
		eligible = true
		if r.active >= r.cfg.Runtime.MaxInflight {
			return -1, true
		}
		st := r.state[t.Source]
		quota := r.quotas[t.Source]
		if a := r.accounts[t.Source]; a != nil && a.active >= a.limit {
			continue
		}
		if r.active >= r.cfg.Runtime.MaxInflight || st.active >= r.cfg.Sources[t.Source].MaxInflight || quota.active >= quota.limit || now.Before(st.cooldown) || now.Before(quota.cooldown) {
			continue
		}
		value := float64(st.active) / float64(r.cfg.Sources[t.Source].MaxInflight)
		switch g.Type {
		case "fallback", "select":
			value = float64(g.members[r.cfg.Sources[t.Source].ID])
		case "latency":
			value = st.latency
		case "auto":
			value += st.latency / 1e6
		}
		if value < score {
			score = value
			best = idx
			// Every policy's score is nonnegative. The first zero in rotated
			// order is therefore also the winner of a complete scan.
			if value == 0 {
				break
			}
		}
	}
	return best, eligible
}
func (r *Router) Acquire(ctx context.Context, q Query) (*Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var queued *waiter
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		if queued != nil {
			r.removeWaiter(queued)
			r.signal()
		}
	}()
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		idx, eligible := r.choose(q, time.Now())
		if idx >= 0 && r.turn(queued) {
			t := r.targets[idx]
			r.active++
			r.state[t.Source].active++
			r.quotas[t.Source].active++
			if a := r.accounts[t.Source]; a != nil {
				a.active++
			}
			r.cursor++
			return &Lease{router: r, Target: t, started: time.Now(), state: r.state[t.Source], quota: r.quotas[t.Source], account: r.accounts[t.Source]}, nil
		}
		if !eligible {
			return nil, ErrUnavailable
		}
		if queued == nil {
			if r.waiting >= r.cfg.Runtime.MaxQueued {
				return nil, ErrQueueFull
			}
			r.waiting++
			queued = &waiter{query: q, ctx: ctx, ready: make(chan struct{}, 1)}
			if r.tail == nil {
				r.head = queued
			} else {
				r.tail.next = queued
			}
			r.tail = queued
		}
		if timer == nil {
			timer = time.NewTimer(250 * time.Millisecond)
		} else {
			timer.Reset(250 * time.Millisecond)
		}
		r.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-queued.ready:
		case <-timer.C:
		}
		timer.Stop()
		r.mu.Lock()
	}
}

// FIFO among requests that can currently run. A saturated source does not
// block an older queued request's unrelated, available source.
func (r *Router) turn(current *waiter) bool {
	for w := r.head; w != nil && w != current; w = w.next {
		if w.ctx.Err() != nil {
			continue
		}
		if i, _ := r.choose(w.query, time.Now()); i >= 0 {
			return false
		}
	}
	return true
}
func (r *Router) removeWaiter(target *waiter) {
	var previous *waiter
	for w := r.head; w != nil; w = w.next {
		if w == target {
			if previous == nil {
				r.head = w.next
			} else {
				previous.next = w.next
			}
			if r.tail == w {
				r.tail = previous
			}
			r.waiting--
			return
		}
		previous = w
	}
}
func (l *Lease) Release(status int, retryAfter time.Duration) {
	l.once.Do(func() {
		r := l.router
		r.mu.Lock()
		defer r.mu.Unlock()
		r.active--
		l.state.active--
		l.quota.active--
		if a := l.account; a != nil {
			a.active--
		}
		st := l.state
		if status == 401 || status == 403 {
			st.blocked = true
		}
		if status == 429 {
			if retryAfter <= 0 {
				retryAfter = 30 * time.Second
			}
			l.quota.cooldown = time.Now().Add(min(retryAfter, 24*time.Hour))
		}
		if status >= 200 && status < 300 {
			st.completed++
			ms := float64(time.Since(l.started).Milliseconds())
			if st.latency == 0 {
				st.latency = ms
			} else {
				st.latency = st.latency*.8 + ms*.2
			}
		} else if status != 499 {
			st.failures++
			if status == 0 || status >= 500 {
				st.cooldown = time.Now().Add(5 * time.Second)
			}
		}
		r.current.signal()
	})
}
func (r *Router) SetEnabled(id string, on bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, s := range r.cfg.Sources {
		if s.ID == id {
			r.state[i].enabled = on
			if on {
				r.state[i].blocked = false
			}
			r.signal()
			return true
		}
	}
	return false
}

type Status struct {
	ID        string    `json:"id"`
	Enabled   bool      `json:"enabled"`
	Active    int       `json:"active"`
	Completed uint64    `json:"completed"`
	Failures  uint64    `json:"failures"`
	Blocked   bool      `json:"blocked"`
	Cooldown  time.Time `json:"cooldown"`
	LatencyMS float64   `json:"latency_ms"`
}

func (r *Router) Status() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.state))
	for i, s := range r.state {
		out = append(out, Status{r.cfg.Sources[i].ID, s.enabled && r.parentEnabled[i], s.active, s.completed, s.failures, s.blocked, maxTime(s.cooldown, r.quotas[i].cooldown), s.latency})
	}
	return out
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func (r *Router) Explain(q Query) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for _, t := range r.targets {
		_, reason := r.eligible(t, q)
		if reason == "" {
			s := r.state[t.Source]
			qt := r.quotas[t.Source]
			switch {
			case r.accounts[t.Source] != nil && r.accounts[t.Source].active >= r.accounts[t.Source].limit:
				reason = "account_capacity"
			case time.Now().Before(maxTime(s.cooldown, qt.cooldown)):
				reason = "cooldown"
			case s.active >= r.cfg.Sources[t.Source].MaxInflight || qt.active >= qt.limit || r.active >= r.cfg.Runtime.MaxInflight:
				reason = "capacity"
			default:
				reason = "eligible"
			}
		}
		out[r.cfg.Sources[t.Source].ID+"/"+t.Model.ID] = reason
	}
	return out
}
