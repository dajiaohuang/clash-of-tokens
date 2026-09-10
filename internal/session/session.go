package session

import "time"

// Metadata is a redacted view of a gateway-managed conversation reference.
// ID is gateway-generated or hashed; upstream identifiers are metadata only.
type Metadata struct {
	ID           string    `json:"id"`
	Source       string    `json:"source"`
	Conversation string    `json:"conversation"`
	Model        string    `json:"model"`
	Protocol     string    `json:"protocol"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
	Expired      bool      `json:"expired"`
	Dirty        bool      `json:"dirty"`
}

// Manager exposes metadata-only inventory and administrative lifecycle actions.
type Manager interface {
	Sessions() ([]Metadata, error)
	ChangeSession(id, action string) error
}
