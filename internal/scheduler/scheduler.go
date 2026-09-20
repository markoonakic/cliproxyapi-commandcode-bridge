// Package scheduler implements the CLIProxyAPI Scheduler capability for
// Command Code.
//
// The host consults a plugin scheduler before its own built-in scheduler, and
// it consults it for EVERY provider. This package therefore claims a decision
// only for Command Code candidates and returns Handled=false otherwise, so
// antigravity, codex and every other provider keep using the host's built-in
// selection unchanged.
//
// Selection mirrors the host's built-in semantics (highest ready priority, then
// round-robin within that priority band) and adds one signal the host cannot
// know before upstream reports it: cached quota exhaustion.
package scheduler

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/identity"
)

// ProviderID is the stable provider key. Candidates for any other provider are
// ignored so the built-in scheduler continues to serve them.
const ProviderID = identity.ProviderKey

// Availability reports whether a credential's cached quota blocks selection.
type Availability interface {
	// Exhausted reports whether the account's quota window is used up and has
	// not reset yet. A false return means "unknown or usable", so an account is
	// never blocked on missing data.
	Exhausted(authID string) bool
}

// Provider implements pluginapi.Scheduler.
type Provider struct {
	availability Availability

	mu         sync.Mutex
	lastPicked string
}

// NewProvider builds a scheduler over the given availability source.
func NewProvider(availability Availability) *Provider {
	return &Provider{availability: availability}
}

// Identifier is unused by the scheduler contract but keeps a stable key.
func (p *Provider) Identifier() string { return ProviderID }

// Pick selects a Command Code credential.
//
// It returns Handled=false in every case where it has no better answer than the
// host, which makes the host fall back to its built-in scheduler:
//   - no Command Code candidates in the request
//   - every Command Code candidate is disabled or unavailable
//   - every candidate is excluded by cached quota exhaustion
//
// Falling back rather than inventing a pick keeps error reporting correct: the
// host knows how to surface a cooldown or auth-unavailable error, and inventing
// one here would replace a precise error with a worse one.
//
// Pick performs no network I/O and no blocking calls, so it stays well inside
// the latency budget the host expects for a scheduling decision.
func (p *Provider) Pick(_ context.Context, req pluginapi.SchedulerPickRequest) (pluginapi.SchedulerPickResponse, error) {
	// Only ever decide for our own provider.
	mine := filterCandidates(req.Candidates, func(candidate pluginapi.SchedulerAuthCandidate) bool {
		return strings.EqualFold(strings.TrimSpace(candidate.Provider), ProviderID)
	})
	if len(mine) == 0 {
		return pluginapi.SchedulerPickResponse{}, nil
	}

	// Skip credentials the host has already marked unusable.
	usable := filterCandidates(mine, selectable)
	if len(usable) == 0 {
		return pluginapi.SchedulerPickResponse{}, nil
	}

	// Skip credentials whose cached quota is exhausted and not yet reset. The
	// host cannot know this before upstream returns a rate-limit error.
	available := filterCandidates(usable, func(candidate pluginapi.SchedulerAuthCandidate) bool {
		return !p.isExhausted(candidate.ID)
	})
	if len(available) == 0 {
		return pluginapi.SchedulerPickResponse{}, nil
	}

	// Highest ready priority band wins, matching the host's built-in ordering.
	best := highestPriority(available)
	band := filterCandidates(available, func(candidate pluginapi.SchedulerAuthCandidate) bool {
		return candidate.Priority == best
	})
	if len(band) == 0 {
		return pluginapi.SchedulerPickResponse{}, nil
	}

	chosen := p.rotate(band)
	if chosen == "" {
		return pluginapi.SchedulerPickResponse{}, nil
	}
	return pluginapi.SchedulerPickResponse{Handled: true, AuthID: chosen}, nil
}

// rotate returns the next credential in a stable round-robin over the band.
//
// Candidates are ordered by ID first, and the successor of the last pick is
// chosen. This matches the host's built-in round-robin, which advances through
// an ID-sorted ring rather than using a count-based modulo, so the rotation is
// stable even when the candidate set changes.
func (p *Provider) rotate(band []pluginapi.SchedulerAuthCandidate) string {
	ordered := append([]pluginapi.SchedulerAuthCandidate(nil), band...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	p.mu.Lock()
	defer p.mu.Unlock()

	start := successorIndex(ordered, p.lastPicked)
	if start >= len(ordered) {
		start = 0
	}
	p.lastPicked = ordered[start].ID
	return ordered[start].ID
}

// successorIndex returns the index of the first candidate ordered after lastID,
// wrapping to the start of the ring.
func successorIndex(ordered []pluginapi.SchedulerAuthCandidate, lastID string) int {
	if lastID == "" {
		return 0
	}
	index := sort.Search(len(ordered), func(i int) bool {
		return ordered[i].ID > lastID
	})
	if index >= len(ordered) {
		return 0
	}
	return index
}

// selectable reports whether the host considers a credential usable.
//
// Status values come from the host's own auth record. An empty status is
// treated as usable so a provider that does not set one is never blocked.
func selectable(candidate pluginapi.SchedulerAuthCandidate) bool {
	switch strings.ToLower(strings.TrimSpace(candidate.Status)) {
	case "disabled", "unavailable", "error", "removed":
		return false
	default:
		return true
	}
}

// highestPriority returns the greatest priority in the candidate set.
func highestPriority(candidates []pluginapi.SchedulerAuthCandidate) int {
	best := candidates[0].Priority
	for _, candidate := range candidates[1:] {
		if candidate.Priority > best {
			best = candidate.Priority
		}
	}
	return best
}

func (p *Provider) isExhausted(authID string) bool {
	if p == nil || p.availability == nil {
		return false
	}
	return p.availability.Exhausted(authID)
}

func filterCandidates(
	candidates []pluginapi.SchedulerAuthCandidate,
	keep func(pluginapi.SchedulerAuthCandidate) bool,
) []pluginapi.SchedulerAuthCandidate {
	out := make([]pluginapi.SchedulerAuthCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.ID) == "" {
			continue
		}
		if keep(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}
