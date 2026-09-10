package upstream

import (
	"clash-of-tokens/internal/chatgptweb"
	"errors"
)

func (c *Client) Sessions() ([]chatgptweb.SessionMetadata, error) {
	if c.web == nil {
		return nil, errors.New("session management is not implemented for this adapter")
	}
	return c.web.Sessions()
}
func (c *Client) ChangeSession(id, action string) error {
	if c.web == nil {
		return errors.New("session management is not implemented for this adapter")
	}
	return c.web.ChangeSession(id, action)
}
