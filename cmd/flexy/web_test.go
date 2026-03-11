package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := secHeaders(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	tests := []struct {
		header, want string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "no-referrer"},
	}
	for _, tc := range tests {
		if got := rec.Header().Get(tc.header); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestHandleAPILogs(t *testing.T) {
	// Write some lines into the global logBuf.
	logBuf = newLogBuffer(10)
	logBuf.Write([]byte(`{"level":"info","msg":"hello"}`))
	logBuf.Write([]byte(`{"level":"info","msg":"world"}`))

	req := httptest.NewRequest(http.MethodGet, "/api/logs?since=1", nil)
	rec := httptest.NewRecorder()
	handleAPILogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Lines   []LogLine `json:"lines"`
		LastSeq int64     `json:"lastSeq"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Lines) != 1 {
		t.Fatalf("got %d lines, want 1 (since=1 should skip seq 0)", len(resp.Lines))
	}
	if resp.Lines[0].Seq != 1 {
		t.Errorf("line seq = %d, want 1", resp.Lines[0].Seq)
	}
}

func TestHandleAPILogs_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	rec := httptest.NewRecorder()
	handleAPILogs(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleAPIConfig_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/config", nil)
	rec := httptest.NewRecorder()
	handleAPIConfig(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleAPIConfig_PostBadJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader("{bad"))
	rec := httptest.NewRecorder()
	handleAPIConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleAPIReconnect(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/reconnect", nil)
	rec := httptest.NewRecorder()
	handleAPIReconnect(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestHandleAPIReconnect_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/reconnect", nil)
	rec := httptest.NewRecorder()
	handleAPIReconnect(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestConnStateString(t *testing.T) {
	tests := []struct {
		state ConnState
		want  string
	}{
		{ConnStateDisconnected, "disconnected"},
		{ConnStateConnecting, "connecting"},
		{ConnStateConnected, "connected"},
		{ConnStateError, "error"},
		{ConnState(99), "unknown"},
	}
	for _, tc := range tests {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("ConnState(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestSetGetConnState(t *testing.T) {
	setConnState(ConnStateDisconnected, nil)
	s, e := getConnState()
	if s != ConnStateDisconnected || e != "" {
		t.Errorf("got (%v, %q), want (disconnected, \"\")", s, e)
	}
}
