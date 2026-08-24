package hiddenrole

import (
	"sort"
	"time"
)

// Timeout constants.
//
// The engine keeps no clock of its own -- when a phase ends is entirely the
// caller's decision (they call EndPhase). These constants and
// PhaseConfig.Timeout are advisory values for the caller, who sets their own
// timer from them.
const (
	// DefaultPhaseTimeout is the fallback suggestion when PhaseConfig.Timeout
	// is not given.
	//
	// It is advice for the caller and the engine does not time anything by
	// it -- when EndPhase is called is entirely up to the caller. Per-phase
	// suggestions are board data and live in the rules package.
	DefaultPhaseTimeout = 30 * time.Second
)

// Config configures the phase machine: where it starts, how phases flow, and
// how long each is expected to take.
//
// Only the three things the phase machine needs. Rules switches (werewolf's
// "may the witch save herself" and the like) do not belong here -- the kernel
// should not recognise those concepts, and they live on a rules package's own
// struct.
//
// It used to be called GameConfig. That name was dropped because it claimed
// too much: it configures the phase machine, not "a game".
type Config struct {
	// StartPhase is the first phase entered after Start. It has no default;
	// Validate requires it.
	StartPhase PhaseType

	// Phases is the per-phase configuration.
	Phases map[PhaseType]*PhaseConfig

	// DefaultTimeout is the suggested timeout when PhaseConfig.Timeout is not
	// given. It is advice and the engine does not time by it -- see the
	// timeout constants above. Use Config.PhaseTimeout(phase) to get the
	// final suggestion for one phase.
	DefaultTimeout time.Duration
}

// PhaseTimeout is the suggested timeout for one phase.
//
// A phase without its own falls back to DefaultTimeout, and a config without
// that falls back to DefaultPhaseTimeout. These two fields used to be written
// and never read -- a caller wanting the suggestion the engine actually uses
// had to reconstruct the configuration and compare.
func (c *Config) PhaseTimeout(phase PhaseType) time.Duration {
	if pc := c.Phases[phase]; pc != nil && pc.Timeout > 0 {
		return pc.Timeout
	}
	if c.DefaultTimeout > 0 {
		return c.DefaultTimeout
	}
	return DefaultPhaseTimeout
}

// PhaseAction is something the machine does on the way into or out of a
// phase.
//
// These two used to be two booleans on PhaseConfig, EndsRound and
// ClearsRoundVars. They were already entry and exit actions in everything but
// name -- one described what happens when a phase finishes, the other what
// happens before another one begins -- and every new requirement of the same
// kind would have added a third boolean, with the interaction between them
// living nowhere but a comment.
//
// As a named list they gain three things: a compound phase can declare one
// **once** for a whole group (see PhaseConfig.Parent), the next lifetime is a
// new constant rather than a new field, and an unrecognised action is
// rejected by Validate instead of being silently ignored.
type PhaseAction string

const (
	// ActionAdvanceRound moves the round counter on by one. Declared in
	// OnExit: it describes what a phase's ending means.
	//
	// What one round of a game is, only the rules know. The kernel used to
	// guess -- "looping back to StartPhase counts as a new round" -- which
	// holds for werewolf (night -> day -> night) and not for the
	// mission-based games, where the loop is one nomination and the round
	// number silently became a nomination counter.
	ActionAdvanceRound PhaseAction = "ADVANCE_ROUND"

	// ActionClearRoundVars clears all round-scoped state. Declared in
	// OnEnter: it describes how a phase begins.
	//
	// It is separate from ActionAdvanceRound because counting and lifetime
	// are two different things. In werewolf they coincide (a night marker
	// lives exactly one round); in the mission-based games one mission may
	// take five nominations, so the team markers and the round number move on
	// different beats. Welded together, the kernel was one lifetime short and
	// the rules made up the difference by hand.
	ActionClearRoundVars PhaseAction = "CLEAR_ROUND_VARS"
)

// knownActions is the closed set. Validate rejects anything else: an action
// the kernel does not recognise would otherwise be declared, ignored, and
// leave the rules believing state was being cleared when it was not.
var knownActions = map[PhaseAction]bool{
	ActionAdvanceRound:   true,
	ActionClearRoundVars: true,
}

// heldByPendingDetour lists the actions that must not run while a detour is
// still owed, and it is the **one** place this machine departs from the
// transition semantics it otherwise follows (see phaseTree.transitionSets).
// Standard semantics run every exit and entry action of every phase actually
// left and entered; here one action is held back.
//
// There is exactly one mechanical reason, and it applies to exactly one
// action: the pending detour queue lives **inside** the round context, so
// clearing round state would erase a debt that was never settled and the
// exiled hunter's shot would vanish. Two detours pointing at the same phase
// make it reachable -- the machine re-enters that phase to drain the second,
// and if it declared "begin from a clean board" it would wipe the queue on
// the way in.
//
// This table used to be a blanket condition instead. Both actions were
// skipped whenever a detour was pending, on the strength of that one reason
// -- which does not hold for counting: the round number has nothing to do
// with the queue. The counter was collateral, and because the phase that
// declares the increment is never exited a second time, the increment was
// **dropped rather than deferred**: a round in which anybody took a death
// detour did not advance the counter at all. Rules packages compensated by
// declaring the increment on every phase the flow might leave through, which
// is the kernel being one step short and the rules making up the difference.
//
// Any action added later runs by default. Being held is a claim about
// interfering with the queue, and a new action has to earn its place here.
var heldByPendingDetour = map[PhaseAction]bool{
	ActionClearRoundVars: true,
}

// PhaseConfig configures one phase.
//
// A phase is either a **leaf** -- somewhere the machine actually stops, with
// steps to run and an exit to take -- or a **compound phase**, which is
// nothing but a name for a group of phases (see Parent). Validate keeps the
// two apart: a compound phase carries no steps and no exit, and is never a
// transition target.
type PhaseConfig struct {
	Type      PhaseType     // which phase this is
	Steps     []PhaseStep   // its steps, in order
	Timeout   time.Duration // suggested timeout; advice, the engine does not time by it
	NextPhase PhaseType     // the default exit, overridable by a GOTO_PHASE effect

	// Parent is the compound phase this one sits inside, empty for a
	// top-level phase.
	//
	// **"The night as a whole" used to be inexpressible.** The phase graph
	// was flat, so anything true of all four NIGHT_* phases had to be
	// declared four times, and a SpeechProvider asking "is it night" had to
	// enumerate them. Werewolf's night genuinely is one thing containing four
	// things, and the configuration could not say so.
	//
	// Naming a parent buys two things:
	//
	//	OnEnter / OnExit declared once for the whole group, fired exactly
	//	once on the way in and out of it (see gameState.enterPhase)
	//
	//	GameView.InPhase(NIGHT), true throughout, so the rules ask about the
	//	group rather than about a list of members
	//
	// Nesting is allowed to any depth; Validate rejects a cycle.
	Parent PhaseType

	// OnEnter runs, in order, when the machine enters this phase.
	//
	// On a compound phase it runs when the group is entered from outside --
	// not on every move between its members. Going NIGHT_GUARD -> NIGHT_WOLF
	// stays inside NIGHT, so NIGHT's actions do not fire; going DAY ->
	// NIGHT_GUARD enters NIGHT, so they do.
	OnEnter []PhaseAction

	// OnExit runs, in order, when the machine leaves this phase. Same
	// grouping rule as OnEnter, in reverse: a compound phase's exit actions
	// run only when the group is actually left.
	OnExit []PhaseAction
}

// PhaseStep is one step of a phase. Their order is the slice's order.
type PhaseStep struct {
	// Role is which role acts. RoleUnspecified means "every role", and
	// RoleSystem means "no player carries this step".
	Role RoleType

	// Skill is what this step submits.
	//
	// **Leaving it empty means "this role wakes, but takes no action"** -- it
	// only receives information and submits nothing. The One Night minion
	// opening their eyes to see the wolves, the masons recognising each
	// other, the insomniac looking at their own card are all this kind of
	// step: no target, no state change, just "it is your turn to learn
	// something".
	//
	// It mirrors RoleSystem: that one is "this step has no player", this one
	// is "this step has a player, who does not act". Together they complete
	// the four combinations of what a step in a phase can be.
	//
	// An empty step does not appear in AllowedSkills (there is nothing they
	// can submit) and does not enter the readiness decision (there is nothing
	// to satisfy), but it **does appear in PhaseInfo.ActiveRoles** -- the
	// host has to know who to wake, which is the entire reason such a step
	// exists.
	//
	// This was previously inexpressible, so a rules package had to hang a
	// SkillSkip on it as a placeholder -- and SKIP means "declining to act",
	// while they are not declining: there was never an action to decline.
	Skill SkillType

	// Required says whether the phase counts as ready only once this step is
	// done.
	//
	// The engine keeps no clock and will not refuse EndPhase over it -- it
	// only uses it to answer "who has yet to act" in
	// Engine.PhaseReadiness(), leaving the caller to decide between waiting
	// and advancing on a timeout. With no eligible actor at all (the guard is
	// dead, say), the step counts as automatically satisfied.
	Required bool

	// Multiple says whether every eligible actor has to act.
	//
	// true: done only once all of them have submitted (wolves agreeing on a
	// kill, everyone voting).
	// false: any one submission completes it.
	// It affects readiness only; how a phase's Resolver treats repeated
	// submissions is its own business.
	Multiple bool

	// Group is a mutually exclusive alternative group. Steps within one
	// phase sharing a non-empty Group are a pick-one-of set: an actor
	// submitting any one of them completes the whole group.
	//
	// The hunter's "shoot" and "do not shoot" are such a pair: without this
	// field, judging step by step would consider a hunter who submitted SKIP
	// as still owing a SHOOT, and marking both Required as the documentation
	// literally says would leave the phase never ready.
	//
	// It affects readiness only, not skill validation: which skills a phase
	// allows is still decided by all of its steps together.
	Group string

	// AllowDeadTarget says whether this skill may target an eliminated
	// player.
	//
	// By default it may not -- pointing a skill at a corpse is nearly always
	// a mis-submission. The witch's antidote is the exception: the person she
	// wants to save is precisely the one already marked dead tonight.
	//
	// The exception used to be hard-coded by skill name inside the kernel's
	// validation, meaning the kernel recognised "the antidote". It is now
	// data declared by the rules.
	AllowDeadTarget bool
}

// SkillUse records one use of a skill.
//
// Player speech does not go through the skill channel; it is handled by
// Engine.SendMessage, where visibility is routed by phase.
type SkillUse struct {
	PlayerID string    // the player using the skill
	Skill    SkillType // which skill

	// Targets are the skill's targets. The vast majority of skills have one;
	// a few name a whole set at once.
	//
	// This used to be `TargetID string` -- one target. That shape was fixed
	// by a sample size of one: werewolf's nine skills happen to have exactly
	// one target each. The missions package's "nominate a team" names 2-5
	// people at once and could only be split into several submissions, at the
	// cost of readiness being unable to say how many were still missing -- it
	// only knew whether the leader had submitted, and reported Ready=true
	// after one nomination out of two. That is the same class of problem as
	// "AllowedSkills telling an unqualified player he may act": the kernel
	// saying something untrue to a player.
	//
	// A single-target skill writes Targets: []string{"x"} and reads Target().
	Targets []string

	// The fields below are filled in by the Engine on submission; a caller
	// does not set them.
	Phase PhaseType
	Round int
}

// Target is the one target of a single-target skill, or the empty string.
//
// The vast majority of skills have exactly one target, and this saves them
// writing Targets[0] and checking for empty every time. A multi-target skill
// reads Targets directly.
func (u *SkillUse) Target() string {
	if u == nil || len(u.Targets) == 0 {
		return ""
	}
	return u.Targets[0]
}

// Validate checks that the configuration is internally consistent.
//
// The phase graph is data the user can replace, and a dangling NextPhase
// makes the engine silently declare the game over when it reaches it -- that
// class of problem has to surface at construction, not by the game abruptly
// ending in round three.
//
// This checks the shape of the configuration only. Two classes of problem it
// cannot check have homes of their own:
//   - "does every phase have a Resolver" depends on runtime registration and
//     is checked by Engine.Start;
//   - the dynamic transitions a detour brings (the phase a Resolver's
//     NewDetourEffect points at) are edges known only at runtime, and are
//     checked by the engine before enqueueing -- a detour whose destination
//     is not in the configuration is vetoed on the spot and logged as an
//     error, rather than carrying the game into an empty phase.
func (c *Config) Validate() error {
	if c == nil {
		return WrapError(CodeInvalidConfig, "config must not be nil")
	}
	if len(c.Phases) == 0 {
		return WrapError(CodeInvalidConfig, "config contains no phases")
	}

	// StartPhase has to be given explicitly: the kernel has no default board,
	// and therefore no default first phase. Leaving it empty used to fall back
	// to NIGHT_GUARD -- which is werewolf's first phase.
	if c.StartPhase == PhaseUnspecified {
		return WrapError(CodeInvalidConfig,
			"config must declare StartPhase: the kernel has no default")
	}
	// A round boundary is only required of a phase graph that **loops**; see
	// loops().
	if c.loops() {
		if !c.hasRoundBoundary() {
			return WrapError(CodeInvalidConfig,
				"no phase declares %s: the round would never advance", ActionAdvanceRound)
		}
		if !c.hasVarReset() {
			return WrapError(CodeInvalidConfig,
				"no phase declares %s: round-scoped state would never reset", ActionClearRoundVars)
		}
	}
	if _, ok := c.Phases[c.StartPhase]; !ok {
		return WrapError(CodeInvalidPhase,
			"start phase %v is not present in config", c.StartPhase)
	}

	composite := c.compositePhases()
	if composite[c.StartPhase] {
		return WrapError(CodeInvalidPhase,
			"start phase %v is a compound phase; start at one of its members", c.StartPhase)
	}

	// Walk the phases in a fixed order. Map iteration order is random, so
	// which of several problems gets reported used to change from run to run
	// -- an unpleasant thing to hit while working out why a board is invalid.
	for _, phaseType := range c.sortedPhases() {
		pc := c.Phases[phaseType]
		if pc == nil {
			return WrapError(CodeInvalidPhase,
				"phase %v has a nil config", phaseType)
		}
		if pc.Type != phaseType {
			return WrapError(CodeInvalidPhase,
				"phase %v is registered under key %v", pc.Type, phaseType)
		}
		if err := c.validateActions(phaseType, pc); err != nil {
			return err
		}
		if err := c.validateParent(phaseType, pc, composite); err != nil {
			return err
		}

		// A compound phase is a name for a group, not a stop on the way. It
		// carries no steps and no exit, and nothing transitions to it -- were
		// it a target, the machine would sit in a state with no resolver and
		// no way out. Entering "the night" means entering one of the night's
		// phases.
		if composite[phaseType] {
			if len(pc.Steps) > 0 {
				return WrapError(CodeInvalidPhase,
					"compound phase %v declares steps; steps belong to its members", phaseType)
			}
			if pc.NextPhase != PhaseUnspecified {
				return WrapError(CodeInvalidPhase,
					"compound phase %v declares NextPhase; an exit belongs to its members", phaseType)
			}
			continue
		}

		// A dangling NextPhase ends the game silently mid-way.
		//
		// UNSPECIFIED is rejected along with it: PhaseEnd exists for saying
		// "the game ends here", so leaving it empty can only be an omission,
		// and an omission has exactly the same consequence as a dangling one.
		if pc.NextPhase == PhaseUnspecified {
			return WrapError(CodeInvalidPhase,
				"phase %v has no NextPhase (use PhaseEnd to end the game)", phaseType)
		}
		if pc.NextPhase != PhaseEnd {
			if _, ok := c.Phases[pc.NextPhase]; !ok {
				return WrapError(CodeInvalidPhase,
					"phase %v points to %v which is not present in config", phaseType, pc.NextPhase)
			}
			if composite[pc.NextPhase] {
				return WrapError(CodeInvalidPhase,
					"phase %v points to compound phase %v; name one of its members",
					phaseType, pc.NextPhase)
			}
		}

		if err := validateSteps(phaseType, pc.Steps); err != nil {
			return err
		}
	}

	return nil
}

// sortedPhases lists every configured phase in a fixed order.
func (c *Config) sortedPhases() []PhaseType {
	out := make([]PhaseType, 0, len(c.Phases))
	for p := range c.Phases {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// compositePhases is the set of phases that some other phase calls its
// parent.
//
// Being a compound phase is not declared, it is **observed**: a phase becomes
// one by being named. A separate boolean would be a second source for one
// fact, and the two could disagree.
func (c *Config) compositePhases() map[PhaseType]bool {
	out := make(map[PhaseType]bool, len(c.Phases))
	for _, pc := range c.Phases {
		if pc != nil && pc.Parent != PhaseUnspecified {
			out[pc.Parent] = true
		}
	}
	return out
}

// validateActions rejects an action the kernel does not recognise.
//
// Declared and ignored is the worst outcome: the rules would believe round
// state was being cleared while it was not, and the bug would surface as a
// spent potion coming back to life several rounds later.
func (c *Config) validateActions(phase PhaseType, pc *PhaseConfig) error {
	for _, group := range []struct {
		where   string
		actions []PhaseAction
	}{{"OnEnter", pc.OnEnter}, {"OnExit", pc.OnExit}} {
		for _, a := range group.actions {
			if !knownActions[a] {
				return WrapError(CodeInvalidPhase,
					"phase %v declares unknown %s action %q", phase, group.where, a)
			}
		}
	}
	return nil
}

// validateParent checks one phase's place in the hierarchy: the parent has to
// exist, has to be a compound phase, and the chain has to terminate.
func (c *Config) validateParent(phase PhaseType, pc *PhaseConfig, composite map[PhaseType]bool) error {
	if pc.Parent == PhaseUnspecified {
		return nil
	}
	if pc.Parent == phase {
		return WrapError(CodeInvalidPhase, "phase %v is its own parent", phase)
	}
	if _, ok := c.Phases[pc.Parent]; !ok {
		return WrapError(CodeInvalidPhase,
			"phase %v names parent %v which is not present in config", phase, pc.Parent)
	}
	if !composite[pc.Parent] {
		// Unreachable in practice -- naming a parent is what makes it one --
		// but stated so the invariant is checked rather than assumed.
		return WrapError(CodeInvalidPhase,
			"phase %v names parent %v which is not a compound phase", phase, pc.Parent)
	}

	// Walk to the root. A cycle would make the transition machinery loop
	// forever while computing which groups are being entered and left, and
	// Validate is the gate that has to catch it -- the same reasoning as
	// loops(), which is bounded for the same reason.
	seen := map[PhaseType]bool{phase: true}
	for at := pc.Parent; at != PhaseUnspecified; {
		if seen[at] {
			return WrapError(CodeInvalidPhase,
				"phase %v sits in a cycle of compound phases", phase)
		}
		seen[at] = true
		next := c.Phases[at]
		if next == nil {
			return WrapError(CodeInvalidPhase,
				"compound phase %v is not present in config", at)
		}
		at = next.Parent
	}
	return nil
}

// validateSteps checks one phase's step declarations.
//
// A duplicate declaration makes AllowedSkills return duplicates and
// PhaseReadiness double-count who has yet to act. RoleUnspecified means
// "every role", so it duplicates any concrete role declaring the same skill
// -- the identical-key half is merely the more conspicuous half of one
// problem.
func validateSteps(phaseType PhaseType, steps []PhaseStep) error {
	type stepKey struct {
		role  RoleType
		skill SkillType
	}

	seen := make(map[stepKey]bool, len(steps))
	allRoles := make(map[SkillType]bool, len(steps))
	groupRole := make(map[string]RoleType, len(steps))
	for _, step := range steps {
		// A mutually exclusive group is "one person picks one of these", so a
		// group spanning roles is meaningless: readiness walks each actor and
		// looks at which member of the group they submitted, and the seer will
		// never submit the witch's skill.
		if step.Group != "" {
			if role, ok := groupRole[step.Group]; ok && role != step.Role {
				return WrapError(CodeInvalidPhase,
					"phase %v group %q spans roles %v and %v",
					phaseType, step.Group, role, step.Role)
			}
			groupRole[step.Group] = step.Role
		}

		key := stepKey{role: step.Role, skill: step.Skill}
		if seen[key] {
			return WrapError(CodeInvalidPhase,
				"phase %v declares %v/%v twice", phaseType, step.Role, step.Skill)
		}
		seen[key] = true
		if step.Role == RoleUnspecified {
			allRoles[step.Skill] = true
		}
	}

	for _, step := range steps {
		if step.Role != RoleUnspecified && allRoles[step.Skill] {
			return WrapError(CodeInvalidPhase,
				"phase %v declares %v for all roles and for %v separately",
				phaseType, step.Skill, step.Role)
		}
	}

	return nil
}

// startPhase returns the starting phase.
//
// It always has a value: Validate forces the configuration to give one.
// Leaving it empty used to fall back to NIGHT_GUARD -- werewolf's first
// phase, and the kernel has no business picking a default for any ruleset.
func (c *Config) startPhase() PhaseType {
	return c.StartPhase
}

// loops reports whether the phase graph goes round in a circle: following the
// default exits, does it ever return to a phase already visited.
//
// This check exists because **One Night Ultimate Werewolf ran into it**: that
// ruleset has one night, one discussion and one vote in the whole game and
// ends at VOTE -- its phase graph is a **straight line**, its round number is
// 1 from start to finish, and that is exactly right.
//
// The hasRoundBoundary and hasVarReset checks used to be unconditional, so
// the kernel, guarding against one class of misconfiguration, forced a
// correct configuration to lie: EndsRound had to be hung on VOTE even though
// no round follows it. The configuration was then misleading whoever read it.
//
// The rule now is: **only a looping graph needs a round boundary**. What
// those two checks really guard against is "round-scoped state is never
// cleared" -- and in a graph that does not loop, each phase is visited once,
// there is no second round, and so there is no risk.
//
// It follows NextPhase only. GOTO_PHASE and the detour queue can both bend
// the flow elsewhere at runtime, but that is the rules deciding at runtime
// and is invisible in the static configuration -- what is judged here is the
// **declared** phase graph, on the same footing as every other Validate
// check.
func (c *Config) loops() bool {
	// A non-looping walk visits each phase **at most** once, so it must reach
	// END within len(Phases) steps (or walk into a phase absent from the
	// configuration, which another check reports). Running out of steps
	// without reaching an end can only mean it loops.
	//
	// It is written this way rather than with a seen table of visited
	// phases: the two give identical answers, and the step cap additionally
	// guarantees that **this function terminates**. Validate is the first
	// gate on the construction path and must never hang on a malformed
	// configuration.
	phase := c.StartPhase
	for i := 0; i <= len(c.Phases); i++ {
		if phase == PhaseUnspecified || phase == PhaseEnd {
			return false
		}
		pc, ok := c.Phases[phase]
		if !ok || pc == nil {
			return false // the graph is broken; another check reports it
		}
		phase = pc.NextPhase
	}
	return true
}

// hasRoundBoundary reports whether any phase declares itself the end of a
// round.
//
// With none, the round number stays at 1 forever and round-scoped state is
// never reset -- in werewolf that means the antidote the witch spent brings
// the same person back night after night, turning a one-shot item into a
// permanent one. Such a configuration is certain to be wrong and should be
// rejected at construction rather than discovered mid-game.
//
// This check is what handing the round boundary to the rules **bought**: when
// the kernel guessed it, there was no way to check whether the guess was
// right; once the rules declare it, it can be checked.
func (c *Config) hasRoundBoundary() bool {
	return c.declaresAction(ActionAdvanceRound)
}

// hasVarReset reports whether any phase declares that it starts from a clean
// board.
//
// With none, round-scoped variables are never cleared -- in werewolf the
// antidote the witch spent saves the same person night after night, turning a
// one-shot item into a permanent one. Like hasRoundBoundary, this check is
// what handing the decision to the rules **bought**: it could not be checked
// while the kernel had it welded in.
func (c *Config) hasVarReset() bool {
	return c.declaresAction(ActionClearRoundVars)
}

// declaresAction reports whether any phase declares this action, on entry or
// on exit.
func (c *Config) declaresAction(want PhaseAction) bool {
	for _, pc := range c.Phases {
		if pc == nil {
			continue
		}
		for _, list := range [][]PhaseAction{pc.OnEnter, pc.OnExit} {
			for _, a := range list {
				if a == want {
					return true
				}
			}
		}
	}
	return false
}
