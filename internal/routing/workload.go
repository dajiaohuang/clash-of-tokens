package routing

// WorkloadStatus reads global lease capacity and the current routing queue under
// one lock. Active includes leases retained by older configuration generations.
type WorkloadStatus struct {
	Active      int `json:"active"`
	ActiveLimit int `json:"active_limit"`
	Queued      int `json:"queued"`
	QueueLimit  int `json:"queue_limit"`
}

func (r *Router) WorkloadStatus() WorkloadStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return WorkloadStatus{Active: r.active, ActiveLimit: r.cfg.Runtime.MaxInflight, Queued: r.waiting, QueueLimit: r.cfg.Runtime.MaxQueued}
}
