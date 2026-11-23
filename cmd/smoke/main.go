// Command smoke checks that a deployed Ogen environment works end to end.
//
//	SMOKE_EMAIL=… SMOKE_PASSWORD=… smoke --base-url https://<env host>
//
// It checks the api health, signs in with a dedicated smoke account, lists
// campaigns, opens the SSE stream until the first heartbeat, uploads a tiny
// image and deletes it again, and signs out.
//
// An unknown device gets a new-device alert email on sign-in. Set
// SMOKE_DEVICE_COOKIE to the smoke account's ogen_device cookie so CI signs in
// as a known device; --print-device-cookie prints one after a local run.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

const deviceCookie = "ogen_device"

func main() {
	log.SetFlags(0)
	log.SetPrefix("smoke: ")

	baseURL := flag.String("base-url", "", "environment origin, e.g. https://ogen-dev.up.railway.app")
	printDevice := flag.Bool("print-device-cookie", false, "print the ogen_device cookie after signing in (local use only)")
	flag.Parse()

	email, password := os.Getenv("SMOKE_EMAIL"), os.Getenv("SMOKE_PASSWORD")
	if *baseURL == "" || email == "" || password == "" {
		log.Fatal("--base-url, SMOKE_EMAIL and SMOKE_PASSWORD are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	s, err := newSmoke(*baseURL, os.Getenv("SMOKE_DEVICE_COOKIE"))
	if err != nil {
		log.Fatal(err)
	}
	if err := s.run(ctx, email, password); err != nil {
		fmt.Fprintf(os.Stderr, "::error::smoke: %v\n", err)
		os.Exit(1)
	}
	if *printDevice {
		fmt.Println(s.cookie(deviceCookie))
	}
	log.Print("all checks passed")
}

type smoke struct {
	base *url.URL
	http *http.Client
}

func newSmoke(baseURL, device string) (*smoke, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("bad --base-url %q", baseURL)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	if device != "" {
		jar.SetCookies(base, []*http.Cookie{{Name: deviceCookie, Value: device, Path: "/"}})
	}
	return &smoke{base: base, http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}, nil
}

// cleanupTimeout bounds each cleanup call. Cleanup gets its own context, so it
// still runs when a failed step used up the run's deadline.
const cleanupTimeout = 30 * time.Second

func (s *smoke) run(ctx context.Context, email, password string) (err error) {
	if err := s.step(ctx, "health", s.health); err != nil {
		return err
	}
	signIn := func(ctx context.Context) error { return s.signIn(ctx, email, password) }
	if err := s.step(ctx, "sign in", signIn); err != nil {
		return err
	}
	// Sign out however the checks below end, and report a failed sign-out.
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		err = errors.Join(err, s.step(ctx, "sign out", s.signOut))
	}()

	for _, st := range []struct {
		name string
		fn   func(context.Context) error
	}{
		{"list campaigns", s.listCampaigns},
		{"event stream", s.events},
		{"upload asset", s.uploadAndDelete},
	} {
		if err := s.step(ctx, st.name, st.fn); err != nil {
			return err
		}
	}
	return nil
}

func (s *smoke) step(ctx context.Context, name string, fn func(context.Context) error) error {
	start := time.Now()
	if err := fn(ctx); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	log.Printf("ok  %-15s %s", name, time.Since(start).Round(time.Millisecond))
	return nil
}

func (s *smoke) health(ctx context.Context) error {
	var body struct {
		Status  string `json:"status"`
		Secrets map[string]struct {
			Present     bool `json:"present"`
			Decryptable bool `json:"decryptable"`
		} `json:"secrets"`
	}
	if err := s.doJSON(ctx, http.MethodGet, "/api/health", nil, http.StatusOK, &body); err != nil {
		return err
	}
	if body.Status != "ok" {
		var bad []string
		for name, sec := range body.Secrets {
			if !sec.Present || !sec.Decryptable {
				bad = append(bad, name)
			}
		}
		return fmt.Errorf("status %q (secrets not usable: %s)", body.Status, strings.Join(bad, ", "))
	}
	return nil
}

func (s *smoke) signIn(ctx context.Context, email, password string) error {
	req := map[string]string{"email": email, "password": password}
	if err := s.doJSON(ctx, http.MethodPost, "/api/sessions", req, http.StatusCreated, nil); err != nil {
		return err
	}
	if s.cookie("c3_session") == "" {
		return errors.New("no session cookie set")
	}
	return nil
}

func (s *smoke) signOut(ctx context.Context) error {
	return s.doJSON(ctx, http.MethodDelete, "/api/sessions", nil, http.StatusNoContent, nil)
}

func (s *smoke) listCampaigns(ctx context.Context) error {
	var campaigns []json.RawMessage
	return s.doJSON(ctx, http.MethodGet, "/api/campaigns", nil, http.StatusOK, &campaigns)
}

// events opens the SSE stream and waits for the heartbeat the server sends on
// connect.
func (s *smoke) events(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url("/api/events?topics=all"), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	// The stream outlives the client's 30s timeout, so use one without it.
	resp, err := (&http.Client{Jar: s.http.Jar}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError(resp)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return fmt.Errorf("content type %q", ct)
	}

	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), ":") {
			return nil
		}
	}
	return cmp.Or(sc.Err(), errors.New("stream closed before the first heartbeat"))
}

// uploadAndDelete uploads a 1×1 PNG and deletes it. The pixel is random so the
// tenant's checksum dedupe doesn't hand back an earlier asset.
func (s *smoke) uploadAndDelete(ctx context.Context) error {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{uint8(rand.N(256)), uint8(rand.N(256)), uint8(rand.N(256)), 255})
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return err
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("files", "smoke.png")
	if err != nil {
		return err
	}
	if _, err := fw.Write(pngBuf.Bytes()); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url("/api/content-bank/assets/upload"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out struct {
		Results []struct {
			AssetID string `json:"asset_id"`
			Status  string `json:"status"`
			Code    string `json:"code"`
			Error   string `json:"error"`
		} `json:"results"`
	}
	if err := s.do(req, http.StatusCreated, &out); err != nil {
		return err
	}
	// The upload answers 201 even when the file failed.
	if len(out.Results) != 1 {
		return fmt.Errorf("%d results, want 1", len(out.Results))
	}
	r := out.Results[0]
	if r.Status != "created" || r.AssetID == "" {
		return fmt.Errorf("upload %s: %s %s", r.Status, r.Code, r.Error)
	}

	// Storage can fail transiently (502); retry so smoke runs don't pile up
	// assets. The delete has its own deadline, so a slow upload can't use it up.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	path := "/api/content-bank/assets/" + url.PathEscape(r.AssetID)
	var delErr error
	for attempt := range 3 {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		if delErr = s.doJSON(ctx, http.MethodDelete, path, nil, http.StatusNoContent, nil); delErr == nil {
			return nil
		}
	}
	return fmt.Errorf("delete asset %s: %w", r.AssetID, delErr)
}

func (s *smoke) doJSON(ctx context.Context, method, path string, in any, wantStatus int, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.url(path), body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.do(req, wantStatus, out)
}

func (s *smoke) do(req *http.Request, wantStatus int, out any) error {
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		return statusError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", req.Method, req.URL.Path, err)
	}
	return nil
}

func (s *smoke) url(path string) string {
	return s.base.String() + path
}

func (s *smoke) cookie(name string) string {
	for _, c := range s.http.Jar.Cookies(s.base) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func statusError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s %s: HTTP %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, bytes.TrimSpace(b))
}
