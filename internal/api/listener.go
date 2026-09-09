package api

import (
	"net"
	"sync"
)

// LimitedListener bounds accepted TCP connections, including clients which
// never send authenticated requests. Backpressure stays in the OS accept queue.
type LimitedListener struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
}

func LimitListener(inner net.Listener, max int) *LimitedListener {
	return &LimitedListener{Listener: inner, slots: make(chan struct{}, max), done: make(chan struct{})}
}
func (l *LimitedListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, e := l.Listener.Accept()
	if e != nil {
		<-l.slots
		return nil, e
	}
	return &limitedConn{Conn: c, release: func() { <-l.slots }}, nil
}
func (l *LimitedListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error { e := c.Conn.Close(); c.once.Do(c.release); return e }
