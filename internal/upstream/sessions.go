package upstream

import (
	"clash-of-tokens/internal/session"
	"errors"
)

func (c *Client) Sessions() ([]session.Metadata, error) {
	if c.web == nil {
		if manager, ok := c.adapter.(session.Manager); ok {
			return manager.Sessions()
		}
		return nil, errors.New("session management is not implemented for this adapter")
	}
	return c.web.Sessions()
}
func (c *Client) ChangeSession(id, action string) error {
	if c.web != nil {
		return c.web.ChangeSession(id, action)
	}
	if manager, ok := c.adapter.(session.Manager); ok {
		return manager.ChangeSession(id, action)
	}
	return errors.New("session management is not implemented for this adapter")
}
