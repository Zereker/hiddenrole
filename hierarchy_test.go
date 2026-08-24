package hiddenrole

import "testing"

// A compound phase is a name for a group of phases. Two things follow from
// declaring one, and both are tested here: entry and exit actions fire for
// the group exactly once, and the rules can ask about the group instead of
// about a list of its members.

// TestTransitionSets_StripsSharedGroups is the rule the whole hierarchy rests
// on: what a move really leaves and really enters is what the two ends do not
// have in common.
func TestTransitionSets_StripsSharedGroups(t *testing.T) {
	tree := testTree()

	cases := []struct {
		name        string
		from, to    PhaseType
		exit, enter []PhaseType
	}{
		{
			name: "between members of one group, the group is not left",
			from: phaseNightGuard, to: phaseNightWolf,
			exit: []PhaseType{phaseNightGuard}, enter: []PhaseType{phaseNightWolf},
		},
		{
			name: "leaving the last member leaves the group, innermost first",
			from: phaseNightResolve, to: phaseDay,
			exit: []PhaseType{phaseNightResolve, phaseNight}, enter: []PhaseType{phaseDay},
		},
		{
			name: "entering the group comes outermost first, so its setup runs before its members'",
			from: phaseVote, to: phaseNightGuard,
			exit: []PhaseType{phaseVote}, enter: []PhaseType{phaseNight, phaseNightGuard},
		},
		{
			// The README's single-phase cycle depends on this: only *strict*
			// ancestors are stripped, so a phase whose exit loops back to
			// itself still leaves and re-enters. Treating it as "no movement"
			// would mean its round never ended.
			name: "a phase looping back to itself still leaves and re-enters",
			from: phaseVote, to: phaseVote,
			exit: []PhaseType{phaseVote}, enter: []PhaseType{phaseVote},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, enter := tree.transitionSets(tc.from, tc.to)
			if !samePhases(exit, tc.exit) {
				t.Errorf("exiting = %v, want %v", exit, tc.exit)
			}
			if !samePhases(enter, tc.enter) {
				t.Errorf("entering = %v, want %v", enter, tc.enter)
			}
		})
	}
}

func samePhases(got, want []PhaseType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestCompoundPhase_ActionsFireOncePerGroup: "the night begins from a clean
// board" is declared once on NIGHT, and must not fire again on every move
// between the night's phases.
//
// This is what the hierarchy buys. Declared on each member instead, a marker
// written during the guard's phase would be wiped before the wolves acted.
func TestCompoundPhase_ActionsFireOncePerGroup(t *testing.T) {
	e := newTestEngine(t, withNoopResolvers()...)
	mustAdd(t, e, "g1", roleGuard)
	mustAdd(t, e, "w1", roleWerewolf)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A marker written in the first phase of the night.
	e.Apply(NewSetVarEffect(ScopeRound, "kill", "w1"))

	// Move through the night. None of these leaves NIGHT, so nothing clears.
	for _, want := range []PhaseType{phaseNightWolf, phaseNightWitch, phaseNightSeer, phaseNightResolve} {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase: %v", err)
		}
		if got := e.Status().Phase; got != want {
			t.Fatalf("phase = %v, want %v", got, want)
		}
		if got := e.Var(ScopeRound, "kill"); got != "w1" {
			t.Fatalf("in %v the round marker was cleared: %q -- the group's entry action fired again", want, got)
		}
	}

	// Now leave the night entirely and come back round to it.
	for _, want := range []PhaseType{phaseDay, phaseVote, phaseNightGuard} {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase: %v", err)
		}
		if got := e.Status().Phase; got != want {
			t.Fatalf("phase = %v, want %v", got, want)
		}
	}
	if got := e.Var(ScopeRound, "kill"); got != "" {
		t.Errorf("re-entering the night should have cleared round state, got %q", got)
	}
}

// TestCompoundPhase_InPhaseAsksAboutTheGroup: the question a SpeechProvider
// actually has is "is it night", not "is the phase one of these four" -- a
// list that every new night phase silently invalidated.
func TestCompoundPhase_InPhaseAsksAboutTheGroup(t *testing.T) {
	e := newTestEngine(t, withNoopResolvers()...)
	mustAdd(t, e, "g1", roleGuard)
	mustAdd(t, e, "w1", roleWerewolf)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	view := e.View()
	if !view.InPhase(phaseNight) {
		t.Error("NIGHT_GUARD sits inside NIGHT")
	}
	if !view.InPhase(phaseNightGuard) {
		t.Error("a phase is inside itself")
	}
	if view.InPhase(phaseDay) {
		t.Error("NIGHT_GUARD does not sit inside DAY")
	}

	// Walk to the day and ask again.
	for e.Status().Phase != phaseDay {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase: %v", err)
		}
	}
	if e.View().InPhase(phaseNight) {
		t.Error("DAY does not sit inside NIGHT")
	}
}

// TestBoard_InPhaseUsesDeclaredAncestry: a board laid out by hand has no
// Config to read the hierarchy from, so a resolver under unit test can state
// it. With none stated it degrades to an equality test, which is the right
// answer for a flat configuration.
func TestBoard_InPhaseUsesDeclaredAncestry(t *testing.T) {
	flat := Board{Phase: phaseNightGuard}.View()
	if flat.InPhase(phaseNight) {
		t.Error("with no ancestry declared, InPhase is an equality test")
	}
	if !flat.InPhase(phaseNightGuard) {
		t.Error("a phase is always inside itself")
	}

	nested := Board{Phase: phaseNightGuard, Ancestry: []PhaseType{phaseNight}}.View()
	if !nested.InPhase(phaseNight) {
		t.Error("the declared ancestry should be honoured")
	}
}

// TestConfig_RejectsABrokenHierarchy: every way of getting the hierarchy
// wrong has to surface at construction. A compound phase reached at runtime
// is a phase with no resolver and no exit -- the game would stop dead.
func TestConfig_RejectsABrokenHierarchy(t *testing.T) {
	const group = PhaseType("GROUP")
	const other = PhaseType("OTHER")

	cases := []struct {
		name   string
		mutate func(c *Config)
	}{
		{
			name: "a parent absent from the config",
			mutate: func(c *Config) {
				c.Phases[phaseDay].Parent = PhaseType("NOWHERE")
			},
		},
		{
			name: "a phase that is its own parent",
			mutate: func(c *Config) {
				c.Phases[phaseDay].Parent = phaseDay
			},
		},
		{
			name: "a cycle of compound phases",
			mutate: func(c *Config) {
				c.Phases[group] = &PhaseConfig{Type: group, Parent: other}
				c.Phases[other] = &PhaseConfig{Type: other, Parent: group}
				c.Phases[phaseDay].Parent = group
			},
		},
		{
			name: "a compound phase carrying steps",
			mutate: func(c *Config) {
				c.Phases[phaseNight].Steps = []PhaseStep{{Role: roleGuard, Skill: skillProtect}}
			},
		},
		{
			name: "a compound phase carrying an exit",
			mutate: func(c *Config) {
				c.Phases[phaseNight].NextPhase = phaseDay
			},
		},
		{
			name: "a transition into a compound phase",
			mutate: func(c *Config) {
				c.Phases[phaseDay].NextPhase = phaseNight
			},
		},
		{
			name: "starting in a compound phase",
			mutate: func(c *Config) {
				c.StartPhase = phaseNight
			},
		},
		{
			name: "an action the kernel does not recognise",
			mutate: func(c *Config) {
				c.Phases[phaseDay].OnEnter = []PhaseAction{PhaseAction("SOMETHING_ELSE")}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("should have been rejected at construction")
			}
		})
	}
}

// TestConfig_ValidateIsDeterministic: which of several problems gets reported
// must not change from run to run. Map iteration order used to decide it,
// which is an unpleasant thing to hit while working out why a board is
// invalid.
func TestConfig_ValidateIsDeterministic(t *testing.T) {
	first := ""
	for i := 0; i < 50; i++ {
		cfg := testConfig()
		cfg.Phases[phaseDay].NextPhase = PhaseType("NOWHERE")
		cfg.Phases[phaseVote].NextPhase = PhaseType("ALSO_NOWHERE")
		err := cfg.Validate()
		if err == nil {
			t.Fatal("should have been rejected")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("Validate reported %q on one run and %q on another", first, err.Error())
		}
	}
}
