package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRetriesKeepTheIdempotencyKey(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "t", "test")
	resp, err := c.Do(context.Background(), Request{Method: http.MethodPost, Path: "/x", Body: map[string]string{"a": "b"}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if resp.Status != http.StatusCreated || len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("status %d, keys %v: a retry must reuse the key", resp.Status, keys)
	}
}

func TestProblemIsParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"https://adaa.no/problems/already-exists","title":"Finnes allerede","status":409,"how_to_resolve":"Bruk den som finnes."}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "t", "test").Do(context.Background(), Request{Method: http.MethodGet, Path: "/x"})
	var p *Problem
	if !errors.As(err, &p) || p.Code() != "already-exists" || p.HowToResolve == "" {
		t.Fatalf("got %#v", err)
	}
}
