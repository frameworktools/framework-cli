package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientMeAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
			return
		}
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("missing headers: %v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"kind": "user", "email": "a@b.edu"})
	}))
	defer srv.Close()

	me, err := NewClient(srv.URL, "good").Me(context.Background())
	if err != nil || me.Who() != "a@b.edu" {
		t.Fatalf("got %+v, %v", me, err)
	}

	_, err = NewClient(srv.URL, "bad").Me(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Message != "Unauthenticated." {
		t.Fatalf("got %v", err)
	}
}

func TestPollDevice(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		polls++
		switch polls {
		case 1:
			w.WriteHeader(http.StatusPreconditionRequired)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
		default:
			_, _ = w.Write([]byte(`{"token":"fw_new","url":"x"}`))
		}
	}))
	defer srv.Close()

	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	token, err := NewClient(srv.URL, "").PollDevice(context.Background(), DeviceCode{DeviceCode: "d", Interval: 5, ExpiresIn: 600}, sleep)
	if err != nil || token != "fw_new" {
		t.Fatalf("got %q, %v", token, err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}
	if len(waits) != 3 || waits[0] != want[0] || waits[1] != want[1] || waits[2] != want[2] {
		t.Errorf("waits %v; want %v", waits, want)
	}
}

func TestPollDeviceStops(t *testing.T) {
	for status, want := range map[int]error{http.StatusForbidden: ErrDeviceDenied, http.StatusGone: ErrDeviceExpired} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		noSleep := func(context.Context, time.Duration) error { return nil }
		_, err := NewClient(srv.URL, "").PollDevice(context.Background(), DeviceCode{Interval: 1}, noSleep)
		srv.Close()
		if !errors.Is(err, want) {
			t.Errorf("status %d: got %v; want %v", status, err, want)
		}
	}
}
