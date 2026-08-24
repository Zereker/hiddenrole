// fuzz_test.go runs the invariants against the kernel itself.
//
// # Why this exists
//
// RunFuzz is the strongest verification this project has -- seven invariants,
// byte-for-byte comparison across a save/restore and a replay, and a history
// of catching divergences nothing else caught. And it had **no caller in this
// repository**: it was exercised only from the rules packages downstream, so
// the kernel's own CI never ran it, and a change to the kernel could break
// every one of those invariants without a single test going red here.
//
// The ruleset below is deliberately not werewolf. It is the smallest thing
// that exercises the machinery the kernel actually owns:
//
//	a compound phase          so entry/exit actions have a group to fire for
//	an interceptor            so the write path is exercised with one installed
//	a detour                  so the pending queue is drained and re-entered
//	named actors              so the runtime actor list is saved and restored
//	a victory condition       so games finish rather than run to the step cap
//
// Nothing here is a game anybody would play. That is the point: if these
// invariants hold for an arbitrary ruleset, they hold because the kernel
// keeps them, not because a particular game happens to be well behaved.
package enginetest_test

import (
	"math/rand"
	"testing"

	"github.com/Zereker/hiddenrole"
	"github.com/Zereker/hiddenrole/enginetest"
)

const (
	phaseCycle   = hiddenrole.PhaseType("CYCLE")
	phaseAct     = hiddenrole.PhaseType("ACT")
	phaseTally   = hiddenrole.PhaseType("TALLY")
	phaseFallout = hiddenrole.PhaseType("FALLOUT")

	roleRed  = hiddenrole.RoleType("RED")
	roleBlue = hiddenrole.RoleType("BLUE")

	skillStrike = hiddenrole.SkillType("STRIKE")

	eventOut   = hiddenrole.EventType("OUT")
	eventSpare = hiddenrole.EventType("SPARE")

	campRed  = hiddenrole.Camp("RED")
	campBlue = hiddenrole.Camp("BLUE")

	keyShield = "shield"
	keyStruck = "struck"
)

// strike records who was struck. It changes no lives itself: the tally does
// that, one phase later, which is what gives the interceptor something
// produced by somebody else to stand in front of.
func strike(uses []*hiddenrole.SkillUse, _ hiddenrole.GameView) []*hiddenrole.Effect {
	out := make([]*hiddenrole.Effect, 0, len(uses))
	for _, u := range uses {
		if u.Skill != skillStrike || u.Target() == "" {
			continue
		}
		out = append(out, hiddenrole.NewSetVarEffect(
			hiddenrole.ScopeRound.Of(u.Target()), keyStruck, hiddenrole.VarPresent))
	}
	return out
}

// tally eliminates everyone marked this round, in ID order so that the
// effects are decided by the board alone.
func tally(_ []*hiddenrole.SkillUse, view hiddenrole.GameView) []*hiddenrole.Effect {
	var out []*hiddenrole.Effect
	for _, p := range view.AlivePlayers() {
		if view.Var(hiddenrole.ScopeRound.Of(p.ID), keyStruck) == "" {
			continue
		}
		out = append(out,
			hiddenrole.NewEffect(eventOut, "", p.ID),
			hiddenrole.NewSetAliveEffect(p.ID, false))
		// A red player takes a parting shot: a detour, so the victory check
		// and the round boundary both have to wait for the queue to drain.
		if p.Role == roleRed {
			out = append(out, hiddenrole.NewDetourEffect(p.ID, phaseFallout))
		}
	}
	return out
}

// fallout is where a detour lands. It names nobody and resolves to nothing --
// the kernel writes the owed player in as the actor on the way in, and the
// point is that the queue drains and the machine comes back out.
func fallout(_ []*hiddenrole.SkillUse, _ hiddenrole.GameView) []*hiddenrole.Effect {
	return nil
}

// shieldOnce replaces the first elimination aimed at a player carrying a
// shield. It is written against the primitive, so it is independent of what
// caused the death -- and it lives nowhere near the resolver that produced
// it, which is the whole reason interceptors exist.
func shieldOnce(ef *hiddenrole.Effect, view hiddenrole.GameView) []*hiddenrole.Effect {
	alive, ok := ef.SetsAlive()
	if !ok || alive {
		return nil
	}
	if view.Var(hiddenrole.ScopeGame.Of(ef.TargetID), keyShield) == "" {
		return nil
	}
	return []*hiddenrole.Effect{
		hiddenrole.NewEffect(eventSpare, "", ef.TargetID),
		hiddenrole.NewSetVarEffect(hiddenrole.ScopeGame.Of(ef.TargetID), keyShield, ""),
	}
}

// lastSideStanding ends the game when one side is gone.
func lastSideStanding(view hiddenrole.GameView) (bool, hiddenrole.Camp) {
	red, blue := 0, 0
	for _, p := range view.AlivePlayers() {
		if p.Role == roleRed {
			red++
		} else {
			blue++
		}
	}
	switch {
	case red == 0 && blue == 0:
		return true, hiddenrole.CampUnspecified
	case blue == 0:
		return true, campRed
	case red == 0:
		return true, campBlue
	}
	return false, hiddenrole.CampUnspecified
}

func config() *hiddenrole.Config {
	return &hiddenrole.Config{
		StartPhase: phaseAct,
		Phases: map[hiddenrole.PhaseType]*hiddenrole.PhaseConfig{
			// ACT and TALLY are one cycle. Declaring "begin from a clean
			// board" on the group means it fires once per cycle rather than
			// once per phase -- if it fired on ACT alone the marks would
			// survive, and if it fired on both the tally would see nothing.
			phaseCycle: {
				Type:    phaseCycle,
				OnEnter: []hiddenrole.PhaseAction{hiddenrole.ActionClearRoundVars},
			},
			phaseAct: {
				Type:      phaseAct,
				Parent:    phaseCycle,
				Steps:     []hiddenrole.PhaseStep{{Role: hiddenrole.RoleUnspecified, Skill: skillStrike}},
				NextPhase: phaseTally,
			},
			phaseTally: {
				Type:      phaseTally,
				Parent:    phaseCycle,
				NextPhase: phaseAct,
				OnExit:    []hiddenrole.PhaseAction{hiddenrole.ActionAdvanceRound},
			},
			// FALLOUT sits outside the cycle on purpose: a detour into it
			// leaves the group, so coming back re-enters it, and the entry
			// action has to not fire while the queue is still draining.
			phaseFallout: {
				Type:      phaseFallout,
				Steps:     []hiddenrole.PhaseStep{{Role: roleRed, Skill: hiddenrole.SkillSkip}},
				NextPhase: phaseAct,
			},
		},
	}
}

func setup(rng *rand.Rand) enginetest.Game {
	n := 3 + rng.Intn(4) // 3..6 players
	seats := make([]enginetest.Seat, 0, n)
	shielded := map[string]bool{}
	labels := []string{"played"}

	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		// The first two seats are fixed one of each. A board with only one
		// side on it is already decided, and Start rejects it -- correctly,
		// which is why the randomisation has to avoid producing one rather
		// than the check being relaxed.
		role := roleBlue
		switch {
		case i == 0:
			role = roleRed
		case i == 1:
			role = roleBlue
		case rng.Intn(2) == 0:
			role = roleRed
		}
		seats = append(seats, enginetest.Seat{ID: id, Role: role})
		if rng.Intn(3) == 0 {
			shielded[id] = true
			labels = append(labels, "shielded")
		}
	}

	opts := []hiddenrole.EngineOption{
		hiddenrole.WithResolver(phaseAct, hiddenrole.ResolverFunc(strike)),
		hiddenrole.WithResolver(phaseTally, hiddenrole.ResolverFunc(tally)),
		hiddenrole.WithResolver(phaseFallout, hiddenrole.ResolverFunc(fallout)),
		hiddenrole.WithVictoryChecker(hiddenrole.VictoryFunc(lastSideStanding)),
		hiddenrole.WithInterceptor(hiddenrole.InterceptorFunc(shieldOnce)),
		hiddenrole.WithRoleSetup(roleRed, hiddenrole.RoleSetupFunc(
			func(id string, _ hiddenrole.RoleType) map[string]string {
				return seatState(shielded, id, campRed)
			})),
		hiddenrole.WithRoleSetup(roleBlue, hiddenrole.RoleSetupFunc(
			func(id string, _ hiddenrole.RoleType) map[string]string {
				return seatState(shielded, id, campBlue)
			})),
	}

	return enginetest.Game{Config: config(), Options: opts, Seats: seats, Labels: labels}
}

// seatState is what a player sits down with. It has to be a pure function of
// the seat, because RestoreEngine is handed these same options again and the
// two must agree.
func seatState(shielded map[string]bool, id string, camp hiddenrole.Camp) map[string]string {
	vars := map[string]string{hiddenrole.VarCamp: string(camp)}
	if shielded[id] {
		vars[keyShield] = hiddenrole.VarPresent
	}
	return vars
}

// act takes one turn: sometimes a strike, sometimes nothing, and sometimes it
// names the next phase's actors so the runtime actor list gets saved,
// restored and replayed like everything else.
func act(e *hiddenrole.Engine, rng *rand.Rand) {
	alive := e.AlivePlayerIDs()
	if len(alive) == 0 {
		return
	}

	if e.Status().Phase == phaseAct && rng.Intn(4) == 0 {
		e.Apply(hiddenrole.NewSetActorsEffect(phaseAct, alive[:1+rng.Intn(len(alive))]...))
	}

	for _, id := range alive {
		if rng.Intn(3) != 0 {
			continue
		}
		target := alive[rng.Intn(len(alive))]
		//nolint:errcheck // a rejected submission is a legitimate outcome here
		_ = e.SubmitSkillUse(&hiddenrole.SkillUse{
			PlayerID: id,
			Skill:    skillStrike,
			Targets:  []string{target},
		})
	}
}

// TestKernel_HoldsItsInvariants runs the seven invariants over random games.
func TestKernel_HoldsItsInvariants(t *testing.T) {
	enginetest.RunFuzz(t, enginetest.FuzzSpec{
		Games:    200,
		MaxSteps: 60,
		Setup:    setup,
		Act:      act,
		WantEnd:  true,
		MustSee:  []string{"shielded"},
	})
}
