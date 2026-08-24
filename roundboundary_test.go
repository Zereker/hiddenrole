package hiddenrole

import "testing"

// A detour holds back exactly one thing -- clearing round state, because the
// pending queue lives there. Everything else on a transition runs as
// declared. These tests pin both halves of that.

// detourEngine builds a game whose vote eliminates a hunter and files a
// detour, so that the round-ending phase is left with a debt outstanding.
func detourEngine(t *testing.T, cfg *Config) *Engine {
	t.Helper()

	vote := ResolverFunc(func(_ []*SkillUse, _ GameView) []*Effect {
		return []*Effect{
			NewSetAliveEffect("h1", false),
			NewDetourEffect("h1", phaseDayHunter),
		}
	})
	opts := withNoopResolvers()
	opts = append(opts, WithResolver(phaseVote, vote))

	e, err := NewEngine(cfg, opts...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	mustAdd(t, e, "h1", roleHunter)
	mustAdd(t, e, "w1", roleWerewolf)
	mustAdd(t, e, "g1", roleGuard)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for e.Status().Phase != phaseVote {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase: %v", err)
		}
	}
	return e
}

// TestRoundBoundary_SurvivesADetour: the increment must not be lost because
// somebody died on the way out.
//
// It used to be. A detour pending at the moment the round-ending phase was
// left skipped **every** action, counter included -- and since that phase is
// never left a second time, the increment was dropped rather than deferred.
// A round in which anybody took a death detour simply did not advance the
// counter, and boards compensated by declaring the increment on every phase
// the flow might leave through.
func TestRoundBoundary_SurvivesADetour(t *testing.T) {
	e := detourEngine(t, testConfig())
	before := e.Status().Round

	// Leaving VOTE with a detour owed. VOTE declares the increment on exit.
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}
	if got := e.Status().Phase; got != phaseDayHunter {
		t.Fatalf("phase = %v, want the detour's phase %v", got, phaseDayHunter)
	}
	if got := e.Status().Round; got != before+1 {
		t.Errorf("round = %d, want %d: the increment was dropped because a detour was owed", got, before+1)
	}
}

// TestRoundBoundary_CountsOnce: and it must not be counted twice.
//
// This is the other side of the same fix. Boards that worked around the
// dropped increment declared it in two places; with the kernel no longer
// dropping it, doing so would count one round as two.
func TestRoundBoundary_CountsOnce(t *testing.T) {
	e := detourEngine(t, testConfig())
	before := e.Status().Round

	// Through the detour and all the way round to the vote again. The first
	// step is unconditional, or the loop would stop before it started.
	for i := 0; i < 20; i++ {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase: %v", err)
		}
		if e.Status().Phase == phaseVote {
			break
		}
	}
	if e.Status().Phase != phaseVote {
		t.Fatal("never came back round to the vote")
	}
	if got := e.Status().Round; got != before+1 {
		t.Errorf("round = %d after one full cycle, want %d", got, before+1)
	}
}

// TestRoundBoundary_ClearingIsHeldWhileADetourIsOwed: the one action that
// really must wait.
//
// The pending queue lives inside the round context, so clearing it would
// erase a debt that was never settled. Two detours to the same phase make
// this reachable: the machine re-enters that phase to drain the second.
func TestRoundBoundary_ClearingIsHeldWhileADetourIsOwed(t *testing.T) {
	cfg := testConfig()
	// Make the detour's own phase declare "begin from a clean board". Without
	// the guard, draining the first debt would wipe the second.
	cfg.Phases[phaseDayHunter].OnEnter = []PhaseAction{ActionClearRoundVars}

	vote := ResolverFunc(func(_ []*SkillUse, _ GameView) []*Effect {
		return []*Effect{
			NewSetAliveEffect("h1", false),
			NewSetAliveEffect("h2", false),
			NewDetourEffect("h1", phaseDayHunter),
			NewDetourEffect("h2", phaseDayHunter),
		}
	})
	opts := withNoopResolvers()
	opts = append(opts, WithResolver(phaseVote, vote))

	e, err := NewEngine(cfg, opts...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	mustAdd(t, e, "h1", roleHunter)
	mustAdd(t, e, "h2", roleHunter)
	mustAdd(t, e, "w1", roleWerewolf)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for e.Status().Phase != phaseVote {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase: %v", err)
		}
	}

	// Both hunters owed a trip; each must get one.
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}
	if got := e.Status().Phase; got != phaseDayHunter {
		t.Fatalf("phase = %v, want %v for the first debt", got, phaseDayHunter)
	}
	first := actorOf(t, e)

	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}
	if got := e.Status().Phase; got != phaseDayHunter {
		t.Fatalf("phase = %v, want %v for the second debt -- the queue was wiped", got, phaseDayHunter)
	}
	if second := actorOf(t, e); second == first {
		t.Errorf("both trips were for %q; the second debt was lost", first)
	}
}

// actorOf returns the single player the current phase names.
func actorOf(t *testing.T, e *Engine) string {
	t.Helper()
	for _, id := range []string{"h1", "h2", "w1"} {
		if len(e.AllowedSkills(id)) > 0 {
			return id
		}
	}
	t.Fatal("nobody may act in the detour's phase")
	return ""
}
