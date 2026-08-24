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
// somebody died on the way out -- and must not land early either.
//
// Both halves have been wrong. The increment was once held with everything
// else and never run, because the phase that declares it is not left a second
// time, so a round in which anybody took a death detour did not advance at
// all. Letting it run immediately fixed that and broke the other half: the
// counter moved at the vote's exit while round state stayed uncleared until
// two transitions later, and "the round advanced" stopped meaning "the board
// is clean". Werewolf asserts those are the same instant, and its own board
// says why -- the round is not over until the shot has been fired.
//
// So the boundary lands **when the debt clears**, both halves together.
func TestRoundBoundary_SurvivesADetour(t *testing.T) {
	e := detourEngine(t, testConfig())
	before := e.Status().Round

	// Leaving VOTE with a detour owed. The boundary is due but waits.
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}
	if got := e.Status().Phase; got != phaseDayHunter {
		t.Fatalf("phase = %v, want the detour's phase %v", got, phaseDayHunter)
	}
	if got := e.Status().Round; got != before {
		t.Errorf("round = %d while a debt is outstanding, want %d: it landed early", got, before)
	}

	// The debt drains here, and the boundary lands with it.
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}
	if got := e.Status().Round; got != before+1 {
		t.Errorf("round = %d after the debt drained, want %d: the increment was lost", got, before+1)
	}
}

// TestRoundBoundary_CounterAndClearLandTogether: the two halves of a round
// boundary are one instant, detour or no detour.
//
// This is the property werewolf's own invariants assert, and the one that
// caught the version of this code that held only the clear.
func TestRoundBoundary_CounterAndClearLandTogether(t *testing.T) {
	e := detourEngine(t, testConfig())

	// A marker from the round that is ending.
	e.Apply(NewSetVarEffect(ScopeRound, "kill", "w1"))
	round := e.Status().Round

	for i := 0; i < 6; i++ {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase: %v", err)
		}
		advanced := e.Status().Round > round
		clean := e.Var(ScopeRound, "kill") == ""
		if advanced != clean {
			t.Fatalf("step %d in %v: round advanced = %v but board clean = %v -- the two halves came apart",
				i, e.Status().Phase, advanced, clean)
		}
		if advanced {
			return
		}
	}
	t.Fatal("the round never advanced")
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
