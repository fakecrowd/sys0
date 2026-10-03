package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBinaryCacheReusesOldRelease(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte("binary")) }))
	defer srv.Close()
	binCacheMu.Lock()
	binCache = map[string]binCacheEntry{srv.URL: {fetched: time.Now().Add(-48 * time.Hour), body: []byte("cached"), ctype: "application/octet-stream"}}
	binCacheMu.Unlock()
	b, _, err := fetchBinary(srv.URL)
	if err != nil || string(b) != "cached" || requests.Load() != 0 {
		t.Fatalf("unchanged old release downloaded again: body=%q requests=%d err=%v", b, requests.Load(), err)
	}
}

func TestBinaryCacheConcurrentMiss(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(10 * time.Millisecond)
		w.Write([]byte("new"))
	}))
	defer srv.Close()
	binCacheMu.Lock()
	binCache = map[string]binCacheEntry{}
	binCacheMu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := fetchBinary(srv.URL); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("concurrent downloads=%d, want 1", requests.Load())
	}
}

func TestBinaryCacheNewVersionAndPrune(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte(r.URL.Path)) }))
	defer srv.Close()
	binCacheMu.Lock()
	binCache = map[string]binCacheEntry{}
	binCacheMu.Unlock()
	for _, u := range []string{srv.URL + "/v1", srv.URL + "/v1", srv.URL + "/v2"} {
		if _, _, err := fetchBinary(u); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("downloads=%d", requests.Load())
	}
	pruneBinaryCache([]string{srv.URL + "/v2"})
	binCacheMu.Lock()
	defer binCacheMu.Unlock()
	if len(binCache) != 1 || string(binCache[srv.URL+"/v2"].body) != "/v2" {
		t.Fatal("prune did not retain current version")
	}
}

func TestBinaryCacheFailureCanRetry(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("recovered"))
	}))
	defer srv.Close()
	binCacheMu.Lock()
	binCache = map[string]binCacheEntry{}
	binCacheMu.Unlock()
	if _, _, err := fetchBinary(srv.URL); err == nil {
		t.Fatal("expected failure")
	}
	if b, _, err := fetchBinary(srv.URL); err != nil || string(b) != "recovered" {
		t.Fatalf("retry: %q %v", b, err)
	}
}
