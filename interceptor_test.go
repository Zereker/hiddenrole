package hiddenrole

import "testing"

// The interception substrate exists so that a rule can react to something
// **somebody else** is about to do. Every test here is written from that
// angle: the intercepting rule and the rule producing the effect never share
// a resolver, because sharing one is exactly the situation this mechanism
// removes the need for.

const (
	eventFlipped   = EventType("FLIPPED")
	keyIdiotFlip   = "idiot.flipped"
	keyArmourSpent = "armour.spent"
)

// killResolver eliminates whoever it is pointed at. It knows nothing about
// idiots, armour, or any other rule that might object.
func killResolver(target string) Resolver {
	return ResolverFunc(func(_ []*SkillUse, _ GameView) []*Effect {
		return []*Effect{
			NewEffect(eventKill, "wolf", target),
			NewSetAliveEffect(target, false),
		}
	})
}

// idiotSurvives is the case the whole mechanism was built for: written once,
// independent of the cause of death, and living nowhere near the resolver
// that produced it.
func idiotSurvives() Interceptor {
	return InterceptorFunc(func(ef *Effect, view GameView) []*Effect {
		alive, ok := ef.SetsAlive()
		if !ok || alive {
			return nil
		}
		p, found := view.Player(ef.TargetID)
		if !found || p.Role != roleIdiot {
			return nil
		}
		if view.Var(ScopeGame.Of(p.ID), keyIdiotFlip) != "" {
			return nil // the card is already face up; it only works once
		}
		return []*Effect{
			NewEffect(eventFlipped, "", p.ID),
			NewSetVarEffect(ScopeGame.Of(p.ID), keyIdiotFlip, VarPresent),
		}
	})
}

// interceptEngine builds a game whose current phase kills target, with the
// given interceptors installed.
func interceptEngine(t *testing.T, target string, interceptors ...Interceptor) *Engine {
	t.Helper()
	opts := withNoopResolvers()
	opts = append(opts, WithResolver(phaseNightGuard, killResolver(target)))
	for _, i := range interceptors {
		opts = append(opts, WithInterceptor(i))
	}
	e := newTestEngine(t, opts...)
	mustAdd(t, e, "id", roleIdiot)
	mustAdd(t, e, "w1", roleWerewolf)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return e
}

// TestInterceptor_ReplacesAnotherRulesEffect: the death never lands, and what
// replaced it did.
func TestInterceptor_ReplacesAnotherRulesEffect(t *testing.T) {
	e := interceptEngine(t, "id", idiotSurvives())
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}

	if p, _ := e.PlayerInfo("id"); !p.Alive {
		t.Error("the idiot should have survived; the lethal effect was replaced")
	}
	if got := e.Var(ScopeGame.Of("id"), keyIdiotFlip); got != VarPresent {
		t.Errorf("the replacement should have flipped the card, got %q", got)
	}
}

// TestInterceptor_LeavesTheAttemptInTheHistory: an audit trail that showed
// only the replacement would answer "what happened" but not "what was about
// to happen and who stopped it".
func TestInterceptor_LeavesTheAttemptInTheHistory(t *testing.T) {
	e := interceptEngine(t, "id", idiotSurvives())
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}

	var sawAttempt, sawReplacement bool
	for _, ef := range e.EffectLog() {
		if alive, ok := ef.SetsAlive(); ok && !alive && ef.TargetID == "id" {
			if !ef.Canceled {
				t.Error("the replaced death should be recorded as cancelled")
			}
			if ef.Reason == "" {
				t.Error("a cancelled effect should say why")
			}
			sawAttempt = true
		}
		if ef.Type == eventFlipped {
			sawReplacement = true
		}
	}
	if !sawAttempt {
		t.Error("the attempt is missing from the history")
	}
	if !sawReplacement {
		t.Error("the replacement is missing from the history")
	}
}

// TestInterceptor_LeavesOtherEffectsAlone: returning nil is "no opinion", and
// an effect nobody objects to must arrive untouched.
func TestInterceptor_LeavesOtherEffectsAlone(t *testing.T) {
	e := interceptEngine(t, "w1", idiotSurvives())
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}
	if p, _ := e.PlayerInfo("w1"); p.Alive {
		t.Error("the wolf is not an idiot; the death should have landed")
	}
}

// TestInterceptor_Chains: two rules written independently, both standing in
// the path, and the second sees what the first produced.
//
// This is the composability claim in its smallest form. Neither function
// knows the other exists.
func TestInterceptor_Chains(t *testing.T) {
	// armour turns any elimination into a spent marker, once.
	armour := InterceptorFunc(func(ef *Effect, view GameView) []*Effect {
		alive, ok := ef.SetsAlive()
		if !ok || alive {
			return nil
		}
		if view.Var(ScopeGame.Of(ef.TargetID), keyArmourSpent) != "" {
			return nil
		}
		return []*Effect{NewSetVarEffect(ScopeGame.Of(ef.TargetID), keyArmourSpent, VarPresent)}
	})

	// countFlips watches for what the first interceptor emits. It could not
	// have been written at all without a substrate: the flip is produced by
	// another interceptor, not by any resolver.
	var flips int
	counter := InterceptorFunc(func(ef *Effect, _ GameView) []*Effect {
		if ef.Type == eventFlipped {
			flips++
		}
		return nil
	})

	e := interceptEngine(t, "id", idiotSurvives(), counter, armour)
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}

	if flips != 1 {
		t.Errorf("the later interceptor saw %d flips from the earlier one, want 1", flips)
	}
	// The idiot's rule replaced the death first, so the armour never had a
	// death to spend itself on -- registration order decides, and the kernel
	// does not decide it for the rules.
	if got := e.Var(ScopeGame.Of("id"), keyArmourSpent); got != "" {
		t.Errorf("armour should not have been spent, got %q", got)
	}
}

// TestInterceptor_DoesNotResurrectAVeto: an effect the rules already
// cancelled is settled, and offering it round again would open the question
// of who wins a veto -- which this pipeline deliberately does not open.
func TestInterceptor_DoesNotResurrectAVeto(t *testing.T) {
	vetoer := InterceptorFunc(func(ef *Effect, _ GameView) []*Effect {
		if alive, ok := ef.SetsAlive(); ok && !alive {
			ef.Cancel("protected")
		}
		return nil
	})
	var seen int
	watcher := InterceptorFunc(func(ef *Effect, _ GameView) []*Effect {
		if alive, ok := ef.SetsAlive(); ok && !alive {
			seen++
			return []*Effect{NewEffect(eventFlipped, "", ef.TargetID)}
		}
		return nil
	})

	e := interceptEngine(t, "id", vetoer, watcher)
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}
	if seen != 0 {
		t.Errorf("a cancelled effect was offered to a later interceptor %d times", seen)
	}
	if p, _ := e.PlayerInfo("id"); !p.Alive {
		t.Error("the veto should have held")
	}
}

// TestInterceptor_AppliesToApplyToo: Engine.Apply is the same write point,
// not a second one, so a host-level change goes past the interceptors like
// everything else.
func TestInterceptor_AppliesToApplyToo(t *testing.T) {
	e := interceptEngine(t, "w1", idiotSurvives())
	e.Apply(NewSetAliveEffect("id", false))
	if p, _ := e.PlayerInfo("id"); !p.Alive {
		t.Error("Apply bypassed the interceptors; it must not")
	}
}

// TestWithInterceptor_RejectsNil: every other option rejects a nil provider,
// and one codebase should not have two standards.
func TestWithInterceptor_RejectsNil(t *testing.T) {
	if _, err := NewEngine(testConfig(), WithInterceptor(nil)); !HasCode(err, CodeInvalidConfig) {
		t.Errorf("a nil interceptor should be rejected as %v, got %v", CodeInvalidConfig, CodeOf(err))
	}
}
