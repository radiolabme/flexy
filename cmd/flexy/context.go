package main

import "context"

type connKey struct{}
type handlerKey struct{}

func getConn(ctx context.Context) *Conn {
	return ctx.Value(connKey{}).(*Conn) //nolint:errcheck
}
