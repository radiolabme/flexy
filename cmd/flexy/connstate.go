package main

import "sync"

type ConnState int

const (
	ConnStateDisconnected ConnState = iota
	ConnStateConnecting
	ConnStateConnected
	ConnStateError
)

func (s ConnState) String() string {
	switch s {
	case ConnStateDisconnected:
		return "disconnected"
	case ConnStateConnecting:
		return "connecting"
	case ConnStateConnected:
		return "connected"
	case ConnStateError:
		return "error"
	default:
		return "unknown"
	}
}

var connStateMu sync.RWMutex
var connState ConnState = ConnStateDisconnected
var connStateErr string

func setConnState(s ConnState, err error) {
	connStateMu.Lock()
	defer connStateMu.Unlock()
	connState = s
	if err != nil {
		connStateErr = err.Error()
	} else {
		connStateErr = ""
	}
}

func getConnState() (ConnState, string) {
	connStateMu.RLock()
	defer connStateMu.RUnlock()
	return connState, connStateErr
}
