package routing

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/protocol"
)

var ErrUnavailable = errors.New("no eligible source; check protocol, approval, tier, quota and enabled state")
var ErrQueueFull = errors.New("queue is full")

type Target struct {
	Source int
	Model  config.Model
}
type state struct {
	inputTokens      uint64
	outputTokens     uint64
	totalTokens      uint64
	estimatedCost    float64
	costKnown        bool
	executions       uint64
	pricedExecutions uint64
	lastExecution    *protocol.ExecutionResult
	ttftMS           float64
	lastSuccess      time.Time
	lastFailure      time.Time
	lastHTTPStatus   int
	virtualFinish    float64
	enabled          bool
	active           int
	cooldown         time.Time
	failures         uint64
	completed        uint64
	latency          float64
	blocked          bool
}
type quota struct {
	lastDispatch  uint64
	virtualFinish float64
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
	poolStrategies []string
	accountWeights []float64
	hasPools       bool
	waiting        int
	retired        bool
	cursor         uint64
	head, tail     *waiter
}

// Capacity survives configuration generations, including leases held by
// retired routers. A hot update never doubles an account's available slots.
type capacity struct {
	events           []ExecutionEvent
	eventCursor      int
	eventSequence    uint64
	mu               sync.Mutex
	active           int
	dispatchSequence uint64
	sourceStates     map[string]*state
	quotaStates      map[string]*quota
	accountStates    map[string]*quota
	current          *Router
}
type Query struct {
	Probe           bool     `json:"-"`
	Detail          bool     `json:"detail,omitempty"`
	ExcludedSources []string `json:"-"`
	Affinity        string   `json:"affinity,omitempty"`
	Model           string   `json:"model"`
	Protocol        string   `json:"protocol"`
	Tools           bool     `json:"tools"`
	Bytes           int64    `json:"bytes"`
	Stateful        bool     `json:"stateful"`
	Vision          bool     `json:"vision"`
}
type Lease struct {
	protocol    string
	probe       bool
	execution   *protocol.ExecutionResult
	ttftMS      float64
	firstOutput sync.Once
	router      *Router
	Target      Target
	once        sync.Once
	started     time.Time
	state       *state
	quota       *quota
	account     *quota
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
		r.poolStrategies = append(r.poolStrategies, providerConfig[s.Provider].PoolStrategy)
		r.accountWeights = append(r.accountWeights, float64(max(1, accountConfig[s.AccountID].Weight)))
		if accountLimits[s.AccountID] != nil {
			r.hasPools = true
		}
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
	}
	// Group membership order is user-controlled (especially for fallback), so
	// build each group's candidate list from its declared source sequence rather
	// than from the source declaration order in the top-level config.
	for id, g := range r.groups {
		for _, sourceID := range g.Sources {
			for i, t := range r.targets {
				if c.Sources[t.Source].ID == sourceID {
					r.candidates[id] = append(r.candidates[id], i)
				}
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
	if q.Probe && q.Model != s.ID+"/"+t.Model.ID {
		return false, "probe_requires_explicit_source"
	}
	if contains(q.ExcludedSources, s.ID) {
		return false, "already_attempted"
	}
	st := r.state[t.Source]
	if !q.Probe && t.Model.Enabled != nil && !*t.Model.Enabled {
		return false, "model_disabled"
	}
	if !q.Probe && !st.enabled {
		return false, "disabled"
	}
	if !q.Probe && !r.parentEnabled[t.Source] {
		return false, "provider_or_account_disabled"
	}
	if !q.Probe && st.blocked {
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
		if g.RequireVision && !t.Model.Vision {
			return false, "vision_required"
		}
		if g.MaxUSDPerMillion != nil {
			cost, known := t.Model.UnitCost()
			if !known {
				return false, "cost_unknown"
			}
			if cost > *g.MaxUSDPerMillion {
				return false, "cost_ceiling"
			}
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
	var preferred map[string]string
	if r.hasPools && g.Type != "fallback" && g.Type != "select" && g.Type != "weighted" {
		preferred = r.poolPreferences(q, candidates, now)
	}
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
		source := r.cfg.Sources[t.Source]
		if account, ok := preferred[source.Provider]; ok && source.AccountID != "" && account != source.AccountID {
			continue
		}
		quota := r.quotas[t.Source]
		if a := r.accounts[t.Source]; a != nil && a.active >= a.limit {
			continue
		}
		if r.active >= r.cfg.Runtime.MaxInflight || st.active >= r.cfg.Sources[t.Source].MaxInflight || quota.active >= quota.limit || now.Before(st.cooldown) || now.Before(quota.cooldown) {
			continue
		}
		value := float64(st.active) / float64(r.cfg.Sources[t.Source].MaxInflight)
		switch g.Type {
		case "weighted":
			value = st.virtualFinish
		case "fallback", "select":
			value = float64(g.members[r.cfg.Sources[t.Source].ID])
		case "latency":
			value = st.latency
		case "auto":
			value += st.latency / 1e6
		}
		preference := r.comparePreferences(idx, best, g.Preferences)
		if best < 0 || preference < 0 || (preference == 0 && value < score) {
			score = value
			best = idx
			// Every policy's score is nonnegative. The first zero in rotated
			// order is therefore also the winner of a complete scan.
			if value == 0 && len(g.Preferences) == 0 {
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
			r.state[t.Source].virtualFinish += 1 / float64(max(1, r.cfg.Sources[t.Source].Weight))
			r.quotas[t.Source].active++
			if a := r.accounts[t.Source]; a != nil {
				a.active++
				r.dispatchSequence++
				a.lastDispatch = r.dispatchSequence
				a.virtualFinish += 1 / r.accountWeights[t.Source]
			}
			r.cursor++
			return &Lease{router: r, Target: t, protocol: q.Protocol, probe: q.Probe, started: time.Now(), state: r.state[t.Source], quota: r.quotas[t.Source], account: r.accounts[t.Source]}, nil
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

// ReleaseAdministrative returns capacity without recording a generation result.
func (l *Lease) ReleaseAdministrative() { l.Release(-1, 0) }

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
		if status == -1 {
			r.current.signal()
			return
		}
		l.recordEvent(status)
		st := l.state
		st.lastHTTPStatus = status
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
			st.lastSuccess = time.Now().UTC()
			st.completed++
			ms := float64(time.Since(l.started).Milliseconds())
			if st.latency == 0 {
				st.latency = ms
			} else {
				st.latency = st.latency*.8 + ms*.2
			}
		} else if status != 499 {
			st.lastFailure = time.Now().UTC()
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
	Health           string                    `json:"health"`
	InputTokens      uint64                    `json:"input_tokens"`
	OutputTokens     uint64                    `json:"output_tokens"`
	TotalTokens      uint64                    `json:"total_tokens"`
	EstimatedCostUSD float64                   `json:"estimated_cost_usd"`
	CostKnown        bool                      `json:"cost_known"`
	LastExecution    *protocol.ExecutionResult `json:"last_execution,omitempty"`
	TTFTMS           float64                   `json:"ttft_ms"`
	LastSuccess      time.Time                 `json:"last_success"`
	LastFailure      time.Time                 `json:"last_failure"`
	LastHTTPStatus   int                       `json:"last_http_status"`
	ID               string                    `json:"id"`
	Enabled          bool                      `json:"enabled"`
	Active           int                       `json:"active"`
	Completed        uint64                    `json:"completed"`
	Failures         uint64                    `json:"failures"`
	Blocked          bool                      `json:"blocked"`
	Cooldown         time.Time                 `json:"cooldown"`
	LatencyMS        float64                   `json:"latency_ms"`
}

// SimulationCandidate describes one target considered by the read-only
// routing simulator. Order is the router's current evaluation order; Selected
// marks the target that would receive the request if capacity were available.
type SimulationCandidate struct {
	ID       string `json:"id"`
	Order    int    `json:"order"`
	Reason   string `json:"reason"`
	Eligible bool   `json:"eligible"`
	Selected bool   `json:"selected"`
}

// Simulation is an opt-in detailed response for control-plane callers. The
// router does not acquire a lease or contact an upstream while producing it.
type Simulation struct {
	Selected   string                `json:"selected,omitempty"`
	Candidates []SimulationCandidate `json:"candidates"`
}

func (r *Router) Status() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.state))
	now := time.Now()
	for i, s := range r.state {
		var execution *protocol.ExecutionResult
		if s.lastExecution != nil {
			copy := *s.lastExecution
			execution = &copy
		}
		estimatedCost := s.estimatedCost
		if !s.costKnown {
			estimatedCost = 0
		}
		enabled := s.enabled && r.parentEnabled[i]
		cooldown := maxTime(s.cooldown, r.quotas[i].cooldown)
		health := "untested"
		switch {
		case !enabled:
			health = "disabled"
		case s.blocked:
			health = "blocked"
		case now.Before(cooldown):
			health = "cooldown"
		case s.active >= r.cfg.Sources[i].MaxInflight || r.quotas[i].active >= r.quotas[i].limit:
			health = "exhausted"
		case s.failures > 0 && s.completed == 0:
			health = "broken"
		case s.failures > 0:
			health = "degraded"
		case s.completed > 0:
			health = "healthy"
		}
		out = append(out, Status{Health: health, ID: r.cfg.Sources[i].ID, Enabled: enabled, Active: s.active, Completed: s.completed, Failures: s.failures, Blocked: s.blocked, Cooldown: cooldown, LatencyMS: s.latency, TTFTMS: s.ttftMS, LastSuccess: s.lastSuccess, LastFailure: s.lastFailure, LastHTTPStatus: s.lastHTTPStatus, LastExecution: execution, InputTokens: s.inputTokens, OutputTokens: s.outputTokens, TotalTokens: s.totalTokens, EstimatedCostUSD: estimatedCost, CostKnown: s.costKnown})
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
	now := time.Now()
	for _, t := range r.targets {
		out[r.cfg.Sources[t.Source].ID+"/"+t.Model.ID] = r.explainReason(t, q, now)
	}
	return out
}

func (r *Router) explainReason(t Target, q Query, now time.Time) string {
	_, reason := r.eligible(t, q)
	if reason != "" {
		return reason
	}
	s := r.state[t.Source]
	qt := r.quotas[t.Source]
	switch {
	case r.accounts[t.Source] != nil && r.accounts[t.Source].active >= r.accounts[t.Source].limit:
		return "account_capacity"
	case now.Before(maxTime(s.cooldown, qt.cooldown)):
		return "cooldown"
	case s.active >= r.cfg.Sources[t.Source].MaxInflight || qt.active >= qt.limit || r.active >= r.cfg.Runtime.MaxInflight:
		return "capacity"
	default:
		return "eligible"
	}
}

// Simulate returns the ordered targets for a query and the selected target,
// without changing router state. The candidate list follows the same source
// and group ordering used by choose; selection still reflects strategy,
// preference, capacity and health rules.
func (r *Router) Simulate(q Query) Simulation {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	selected, _ := r.choose(q, now)
	key := q.Model
	if strings.HasPrefix(key, "auto/") {
		key = "auto"
	}
	indices := r.candidates[key]
	if len(indices) == 0 {
		indices = make([]int, len(r.targets))
		for i := range r.targets {
			indices[i] = i
		}
	}
	out := Simulation{Candidates: make([]SimulationCandidate, 0, len(indices))}
	for order, idx := range indices {
		t := r.targets[idx]
		id := r.cfg.Sources[t.Source].ID + "/" + t.Model.ID
		reason := r.explainReason(t, q, now)
		out.Candidates = append(out.Candidates, SimulationCandidate{ID: id, Order: order + 1, Reason: reason, Eligible: reason == "eligible", Selected: idx == selected})
		if idx == selected {
			out.Selected = id
		}
	}
	return out
}
