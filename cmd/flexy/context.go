package main

import "context"

type connKey struct{}

func getConn(ctx context.Context) *Conn {
	return ctx.Value(connKey{}).(*Conn) //nolint:errcheck // internal context key, always set
}

type handlerKey struct{}

func getHandler(ctx context.Context) *Handler { //nolint:unused // reserved for future use
	return ctx.Value(handlerKey{}).(*Handler) //nolint:errcheck // internal context key, always set
}
