// Package providerutil contains small bounded concurrency helpers shared by
// native provider adapters.
package providerutil

import (
	"context"
	"sync"
)

// Gate is a zero-value-ready, context-aware exclusive lock. It prevents a
// canceled conversation turn from remaining blocked behind a long generation.
type Gate struct {
	once sync.Once
	token chan struct{}
}

func (g *Gate) Lock(ctx context.Context) error {
	g.once.Do(func(){g.token=make(chan struct{},1)})
	if err:=ctx.Err();err!=nil{return err}
	select {
	case g.token<-struct{}{}:
		if err:=ctx.Err();err!=nil{<-g.token;return err}
		return nil
	case <-ctx.Done(): return ctx.Err()
	}
}

func (g *Gate) Unlock(){<-g.token}
