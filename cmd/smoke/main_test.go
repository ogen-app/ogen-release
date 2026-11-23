package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeAPI mimics the ogen routes the smoke test uses.
type fakeAPI struct {
	mu        sync.Mutex
	health    string
	upload    string // result status
	calls     []string
	deviceHit string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()

	authed := func() bool {
		c, err := r.Cookie("c3_session")
		if err != nil || c.Value != "sess" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}

	switch r.Method + " " + r.URL.Path {
	case "GET /api/health":
		json.NewEncoder(w).Encode(map[string]any{"status": f.health, "secrets": map[string]any{
			"zernio": map[string]bool{"present": true, "decryptable": f.health == "ok"},
		}})
	case "POST /api/sessions":
		if c, err := r.Cookie(deviceCookie); err == nil {
			f.deviceHit = c.Value
		}
		http.SetCookie(w, &http.Cookie{Name: "c3_session", Value: "sess", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: deviceCookie, Value: "dev-1", Path: "/"})
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"sess"}`))
	case "DELETE /api/sessions":
		if authed() {
			w.WriteHeader(http.StatusNoContent)
		}
	case "GET /api/campaigns":
		if authed() {
			w.Write([]byte(`[]`))
		}
	case "GET /api/events":
		if !authed() {
			return
		}
		if r.URL.Query().Get("topics") == "" {
			http.Error(w, "topics required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(": ping\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	case "POST /api/content-bank/assets/upload":
		if !authed() {
			return
		}
		if _, _, err := r.FormFile("files"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"results": []map[string]string{
			{"asset_id": "a1", "status": f.upload, "code": "service_unavailable"},
		}})
	case "DELETE /api/content-bank/assets/a1":
		if authed() {
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		http.NotFound(w, r)
	}
}

func runSmoke(t *testing.T, api *fakeAPI, device string) error {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	s, err := newSmoke(srv.URL+"/", device)
	if err != nil {
		t.Fatal(err)
	}
	return s.run(t.Context(), "smoke@example.com", "pw")
}

func TestSmokePasses(t *testing.T) {
	api := &fakeAPI{health: "ok", upload: "created"}
	if err := runSmoke(t, api, "known-device"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/health", "POST /api/sessions", "GET /api/campaigns", "GET /api/events",
		"POST /api/content-bank/assets/upload", "DELETE /api/content-bank/assets/a1", "DELETE /api/sessions",
	}
	if !slices.Equal(api.calls, want) {
		t.Errorf("calls = %v, want %v", api.calls, want)
	}
	if api.deviceHit != "known-device" {
		t.Errorf("device cookie sent = %q", api.deviceHit)
	}
}

func TestSmokeFailsOnDegradedHealth(t *testing.T) {
	err := runSmoke(t, &fakeAPI{health: "degraded", upload: "created"}, "")
	if err == nil || !strings.Contains(err.Error(), "zernio") {
		t.Fatalf("err = %v", err)
	}
}

func TestSmokeFailsOnFailedUpload(t *testing.T) {
	err := runSmoke(t, &fakeAPI{health: "ok", upload: "failed"}, "")
	if err == nil || !strings.Contains(err.Error(), "service_unavailable") {
		t.Fatalf("err = %v", err)
	}
}
