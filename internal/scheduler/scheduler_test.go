package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// fakeAvailability is a controllable exhaustion source.
type fakeAvailability struct {
	exhausted map[string]bool
}

func (f *fakeAvailability) Exhausted(authID string) bool { return f.exhausted[authID] }

func candidate(id, provider string, priority int, status string) pluginapi.SchedulerAuthCandidate {
	return pluginapi.SchedulerAuthCandidate{ID: id, Provider: provider, Priority: priority, Status: status}
}

func pick(t *testing.T, p *Provider, candidates ...pluginapi.SchedulerAuthCandidate) (pluginapi.SchedulerPickResponse, bool) {
	t.Helper()
	resp, err := p.Pick(context.Background(), pluginapi.SchedulerPickRequest{
		Provider: ProviderID, Candidates: candidates,
	})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	return resp, resp.Handled
}

// Covers acceptance criterion 4.1: the highest priority active account wins.
func TestPickSelectsHighestPriority(t *testing.T) {
	p := NewProvider(&fakeAvailability{})
	resp, handled := pick(t, p,
		candidate("a", ProviderID, 5, "active"),
		candidate("b", ProviderID, 6, "active"),
		candidate("c", ProviderID, 3, "active"),
	)
	if !handled {
		t.Fatal("expected a handled pick")
	}
	if resp.AuthID != "b" {
		t.Errorf("picked %q, want the highest priority candidate b", resp.AuthID)
	}
}

// Covers 4.2 (first half): cooled or unavailable accounts are skipped.
func TestPickSkipsUnusableStatuses(t *testing.T) {
	for _, status := range []string{"disabled", "unavailable", "error", "removed", "DISABLED"} {
		p := NewProvider(&fakeAvailability{})
		resp, handled := pick(t, p,
			candidate("bad", ProviderID, 9, status),
			candidate("good", ProviderID, 4, "active"),
		)
		if !handled || resp.AuthID != "good" {
			t.Errorf("status %q: picked %q handled=%v, want good", status, resp.AuthID, handled)
		}
	}
}

// Covers 4.2 (second half): an account whose quota is exhausted is bypassed.
func TestPickSkipsQuotaExhaustedAccounts(t *testing.T) {
	availability := &fakeAvailability{exhausted: map[string]bool{"big": true}}
	p := NewProvider(availability)
	resp, handled := pick(t, p,
		candidate("big", ProviderID, 9, "active"),
		candidate("small", ProviderID, 4, "active"),
	)
	if !handled || resp.AuthID != "small" {
		t.Fatalf("picked %q handled=%v, want small (the priority-9 account is exhausted)", resp.AuthID, handled)
	}

	// When exhaustion clears, the higher-priority account is used again without
	// any restart or background timer.
	availability.exhausted["big"] = false
	resp, _ = pick(t, p, candidate("big", ProviderID, 9, "active"), candidate("small", ProviderID, 4, "active"))
	if resp.AuthID != "big" {
		t.Errorf("after reset picked %q, want big back in rotation", resp.AuthID)
	}
}

// Covers 4.3: equal-priority accounts rotate, and the rotation is a stable
// successor walk over ID-sorted candidates rather than a count-based modulo.
func TestPickRotatesWithinEqualPriority(t *testing.T) {
	p := NewProvider(&fakeAvailability{})
	band := []pluginapi.SchedulerAuthCandidate{
		candidate("a", ProviderID, 6, "active"),
		candidate("b", ProviderID, 6, "active"),
		candidate("c", ProviderID, 6, "active"),
	}

	// Candidates arrive unsorted; rotation must still be over the sorted ring.
	var order []string
	for i := 0; i < 4; i++ {
		resp, handled := pick(t, p, band[1], band[2], band[0])
		if !handled {
			t.Fatal("expected a handled pick")
		}
		order = append(order, resp.AuthID)
	}
	want := []string{"a", "b", "c", "a"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("rotation = %v, want %v", order, want)
		}
	}
}

// A changing candidate set must not desynchronise the rotation: the successor
// is found by ID, so removing a candidate does not repeat or skip another.
func TestRotationSurvivesChangingCandidateSet(t *testing.T) {
	p := NewProvider(&fakeAvailability{})
	all := []pluginapi.SchedulerAuthCandidate{
		candidate("a", ProviderID, 6, "active"),
		candidate("b", ProviderID, 6, "active"),
		candidate("c", ProviderID, 6, "active"),
	}

	if resp, _ := pick(t, p, all...); resp.AuthID != "a" {
		t.Fatalf("first pick = %q, want a", resp.AuthID)
	}
	// "b" disappears; the successor of "a" is now "c".
	withoutB := []pluginapi.SchedulerAuthCandidate{all[0], all[2]}
	if resp, _ := pick(t, p, withoutB...); resp.AuthID != "c" {
		t.Errorf("after removing b picked %q, want c", resp.AuthID)
	}
}

// The scheduler is consulted for every provider, so it must decline anything
// that is not Command Code and let the host's built-in selection run.
func TestPickDeclinesForeignProviders(t *testing.T) {
	p := NewProvider(&fakeAvailability{})
	for _, providers := range [][]pluginapi.SchedulerAuthCandidate{
		{candidate("a", "antigravity", 9, "active")},
		{candidate("a", "openai-codex", 9, "active")},
		{candidate("a", "claude", 9, "active")},
		{candidate("a", "", 9, "active")},
		{},
	} {
		if _, handled := pick(t, p, providers...); handled {
			t.Errorf("declined expected for candidates %+v", providers)
		}
	}
}

// A mixed request must still decide for our candidate and ignore the others.
func TestPickHandlesMixedProviders(t *testing.T) {
	p := NewProvider(&fakeAvailability{})
	resp, handled := pick(t, p,
		candidate("other", "antigravity", 9, "active"),
		candidate("ours", ProviderID, 2, "active"),
	)
	if !handled || resp.AuthID != "ours" {
		t.Fatalf("picked %q handled=%v, want ours", resp.AuthID, handled)
	}
}

// When nothing is selectable the scheduler must decline rather than invent a
// pick, so the host can report its own precise cooldown or unavailable error.
func TestPickDeclinesWhenAllUnusable(t *testing.T) {
	availability := &fakeAvailability{exhausted: map[string]bool{"a": true, "b": true}}
	p := NewProvider(availability)
	if _, handled := pick(t, p,
		candidate("a", ProviderID, 6, "active"),
		candidate("b", ProviderID, 6, "active"),
	); handled {
		t.Error("expected a decline when every candidate is exhausted")
	}

	empty := NewProvider(&fakeAvailability{})
	if _, handled := pick(t, empty, candidate("x", ProviderID, 6, "disabled")); handled {
		t.Error("expected a decline when every candidate is disabled")
	}
}

// A single account must still be selected, and repeatedly.
func TestPickSingleAccount(t *testing.T) {
	p := NewProvider(&fakeAvailability{})
	for i := 0; i < 3; i++ {
		resp, handled := pick(t, p, candidate("only", ProviderID, 6, "active"))
		if !handled || resp.AuthID != "only" {
			t.Fatalf("pick %d = %q handled=%v", i, resp.AuthID, handled)
		}
	}
}

// Candidates without an ID are skipped, because the host cannot resolve them.
func TestPickSkipsCandidatesWithoutID(t *testing.T) {
	p := NewProvider(&fakeAvailability{})
	resp, handled := pick(t, p,
		candidate("", ProviderID, 9, "active"),
		candidate("real", ProviderID, 1, "active"),
	)
	if !handled || resp.AuthID != "real" {
		t.Fatalf("picked %q handled=%v, want real", resp.AuthID, handled)
	}
}

// An empty status must be treated as usable, so a provider that does not set a
// status is never blocked.
func TestPickTreatsEmptyStatusAsUsable(t *testing.T) {
	p := NewProvider(&fakeAvailability{})
	if resp, handled := pick(t, p, candidate("a", ProviderID, 6, "")); !handled || resp.AuthID != "a" {
		t.Errorf("empty status picked %q handled=%v", resp.AuthID, handled)
	}
}

// Covers 4.4: Pick performs no network or blocking work, so it is fast.
func TestPickIsFast(t *testing.T) {
	availability := &fakeAvailability{}
	p := NewProvider(availability)
	band := make([]pluginapi.SchedulerAuthCandidate, 0, 64)
	for i := 0; i < 64; i++ {
		band = append(band, candidate(string(rune('a'+i%26))+string(rune('a'+i/26)), ProviderID, 6, "active"))
	}
	start := time.Now()
	for i := 0; i < 5000; i++ {
		_, _ = p.Pick(context.Background(), pluginapi.SchedulerPickRequest{Provider: ProviderID, Candidates: band})
	}
	perCall := time.Since(start) / 5000
	if perCall > time.Millisecond {
		t.Errorf("Pick averaged %v per call, want under 1ms", perCall)
	}
}

// A nil availability source must not panic and must not block selection.
func TestNilAvailabilityIsSafe(t *testing.T) {
	p := NewProvider(nil)
	resp, handled := pick(t, p, candidate("a", ProviderID, 6, "active"))
	if !handled || resp.AuthID != "a" {
		t.Errorf("picked %q handled=%v with no availability source", resp.AuthID, handled)
	}
}
