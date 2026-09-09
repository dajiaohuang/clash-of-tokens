package providerutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCanceledWaiterDoesNotWaitForOwner(t *testing.T){
	var g Gate
	if err:=g.Lock(context.Background());err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithCancel(context.Background())
	done:=make(chan error,1)
	go func(){done<-g.Lock(ctx)}()
	cancel()
	select{case err:=<-done:if !errors.Is(err,context.Canceled){t.Fatal(err)};case <-time.After(time.Second):t.Fatal("canceled waiter remained blocked")}
	g.Unlock()
	ctx2,cancel2:=context.WithTimeout(context.Background(),time.Second);defer cancel2()
	if err:=g.Lock(ctx2);err!=nil{t.Fatal("canceled waiter retained capacity",err)}
	g.Unlock()
}
