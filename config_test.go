package hiddenrole

import "testing"

// TestValidate_RoundBoundaryRequiredOnlyWhenTheGraphLoops
// A round boundary is required only of a phase graph that loops.
//
// This one came out of the third rules package (One Night Ultimate
// Werewolf): that ruleset has one night, one discussion and one vote in the
// whole game, its phase graph is **a straight line**, and its round number is
// 1 from start to finish -- which is exactly right. The check used to be
// unconditional, so the kernel, guarding against one class of
// misconfiguration, forced a correct configuration to lie (hanging EndsRound
// on the last phase even though no round follows it).
func TestValidate_RoundBoundaryRequiredOnlyWhenTheGraphLoops(t *testing.T) {
	phaseA, phaseB := PhaseType("A"), PhaseType("B")
	step := []PhaseStep{{Role: roleVillager, Skill: skillVote}}

	t.Run("a straight-line graph needs no round boundary", func(t *testing.T) {
		cfg := &Config{
			StartPhase: phaseA,
			Phases: map[PhaseType]*PhaseConfig{
				phaseA: {Type: phaseA, Steps: step, NextPhase: phaseB},
				phaseB: {Type: phaseB, Steps: step, NextPhase: PhaseEnd},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("a graph that ends at END should not be required to declare a round boundary: %v", err)
		}
	})

	t.Run("a looping graph still does", func(t *testing.T) {
		cfg := &Config{
			StartPhase: phaseA,
			Phases: map[PhaseType]*PhaseConfig{
				phaseA: {Type: phaseA, Steps: step, NextPhase: phaseB},
				phaseB: {Type: phaseB, Steps: step, NextPhase: phaseA}, // loops back
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("a looping graph with no round boundary leaves the round at 1 and round variables never cleared; it should be rejected")
		}
		if !HasCode(err, CodeInvalidConfig) {
			t.Errorf("error code should be %v, got %v", CodeInvalidConfig, CodeOf(err))
		}
	})

	t.Run("EndsRound alone is not enough for a looping graph", func(t *testing.T) {
		cfg := &Config{
			StartPhase: phaseA,
			Phases: map[PhaseType]*PhaseConfig{
				phaseA: {Type: phaseA, Steps: step, NextPhase: phaseB},
				phaseB: {Type: phaseB, Steps: step, NextPhase: phaseA, EndsRound: true},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Error("the round number would rise while round variables are never cleared; that should be rejected too")
		}
	})

	t.Run("a phase pointing at itself is a loop", func(t *testing.T) {
		cfg := &Config{
			StartPhase: phaseA,
			Phases: map[PhaseType]*PhaseConfig{
				phaseA: {Type: phaseA, Steps: step, NextPhase: phaseA},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Error("a phase pointing at itself is a loop")
		}
	})

	t.Run("all three real rules packages pass", func(t *testing.T) {
		// The first two loop and declare a round boundary; the third is a
		// straight line and declares none.
		if err := testConfig().Validate(); err != nil {
			t.Errorf("the looping board the kernel tests use: %v", err)
		}
	})
}

// TestValidate_RejectsLifecyclePhases: START and END are the kernel's own
// lifecycle, and a config may use neither as a phase of play.
//
// Both configurations below used to pass Validate, and both produce a game
// that is broken **silently** -- which is the one outcome this function
// exists to prevent (its own doc comment says so: a dangling NextPhase has to
// surface at construction, not by the game ending in round three).
//
// What they actually did, measured before the check was added:
//
//	StartPhase = START      Start() succeeded and left Phase at START, so a
//	                        second Start() also succeeded, AddPlayer still
//	                        succeeded after the game had begun, and every
//	                        EndPhase answered "game not started". The game
//	                        could never move, and nothing ever said why.
//	a phase keyed END       the game reached END and stopped, while that
//	                        entry went on answering AllowedSkills and
//	                        SubmitSkillUse kept **accepting** submissions --
//	                        into a phase that can never resolve.
func TestValidate_RejectsLifecyclePhases(t *testing.T) {
	phaseA := PhaseType("A")
	step := []PhaseStep{{Role: roleVillager, Skill: skillVote}}

	t.Run("StartPhase must not be START", func(t *testing.T) {
		cfg := &Config{
			StartPhase: PhaseStart,
			Phases: map[PhaseType]*PhaseConfig{
				phaseA: {Type: phaseA, Steps: step, NextPhase: PhaseEnd},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Error("StartPhase = START should be rejected: the game could never advance")
		} else if !HasCode(err, CodeInvalidConfig) {
			t.Errorf("want INVALID_CONFIG, got %v (%v)", CodeOf(err), err)
		}
	})

	t.Run("StartPhase must not be END", func(t *testing.T) {
		// PhaseEnd is in Phases here on purpose. Without it, "the start phase
		// is not present in the config" would reject this configuration for
		// an unrelated reason, and the subtest would pass whether the rule
		// under test exists or not.
		cfg := &Config{
			StartPhase: PhaseEnd,
			Phases: map[PhaseType]*PhaseConfig{
				phaseA:   {Type: phaseA, Steps: step, NextPhase: PhaseEnd},
				PhaseEnd: {Type: PhaseEnd, Steps: step, NextPhase: PhaseEnd},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Error("StartPhase = END should be rejected: the game would begin over")
		}
	})

	t.Run("no phase may be keyed START or END", func(t *testing.T) {
		for _, lifecycle := range []PhaseType{PhaseStart, PhaseEnd} {
			cfg := &Config{
				StartPhase: phaseA,
				Phases: map[PhaseType]*PhaseConfig{
					phaseA:    {Type: phaseA, Steps: step, NextPhase: PhaseEnd},
					lifecycle: {Type: lifecycle, Steps: step, NextPhase: PhaseEnd},
				},
			}
			if err := cfg.Validate(); err == nil {
				t.Errorf("a phase keyed %v should be rejected: it is never resolved", lifecycle)
			} else if !HasCode(err, CodeInvalidPhase) {
				t.Errorf("%v: want INVALID_PHASE, got %v (%v)", lifecycle, CodeOf(err), err)
			}
		}
	})

	t.Run("ending the game with NextPhase: PhaseEnd is still fine", func(t *testing.T) {
		cfg := &Config{
			StartPhase: phaseA,
			Phases: map[PhaseType]*PhaseConfig{
				phaseA: {Type: phaseA, Steps: step, NextPhase: PhaseEnd},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("END as a destination is the documented way to end a game: %v", err)
		}
	})
}
