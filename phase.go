package hiddenrole

// phaseManager owns the phase configuration and its resolvers.
type phaseManager struct {
	config    *Config
	tree      *phaseTree
	resolvers map[PhaseType]Resolver
}

// newPhaseManager builds a phase manager.
//
// It installs no default resolvers: the kernel does not know which phase is
// resolved by whom. Werewolf's are passed in as construction options by
// werewolf.Options.
func newPhaseManager(config *Config) *phaseManager {
	return &phaseManager{
		config:    config,
		tree:      newPhaseTree(config),
		resolvers: make(map[PhaseType]Resolver, 8),
	}
}

// registerResolver registers or replaces one phase's resolver.
func (p *phaseManager) registerResolver(phase PhaseType, r Resolver) {
	p.resolvers[phase] = r
}

// validateResolvers checks that every phase the machine can stop in has a
// resolver.
//
// A missing resolver raises no error at play time; it just silently drops the
// skills submitted in that phase -- a failure that is nearly impossible to
// locate mid-game, so it has to be caught before the game starts.
//
// A compound phase is skipped: it is a name for a group, never a transition
// target, and nothing is ever submitted in it. Demanding one would force
// every ruleset that groups its phases to register a resolver that could not
// be called -- which is how this was first noticed, with the test board's
// NIGHT failing to start a game.
func (p *phaseManager) validateResolvers() error {
	for _, phaseType := range p.config.sortedPhases() {
		if p.tree.isComposite(phaseType) {
			continue
		}
		if p.resolvers[phaseType] == nil {
			return WrapError(CodeInvalidPhase,
				"phase %v has no resolver registered", phaseType)
		}
	}
	return nil
}

// phaseConfig returns a phase's configuration.
func (p *phaseManager) phaseConfig(phase PhaseType) *PhaseConfig {
	return p.config.Phases[phase]
}

// resolver returns a phase's resolver.
func (p *phaseManager) resolver(phase PhaseType) Resolver {
	return p.resolvers[phase]
}

// stepFor finds the step declaration matching "this role, in this phase,
// using this skill".
//
// It shares one rule with allowedSkills: RoleUnspecified means "every role".
// No match means the skill is not allowed right now.
func (p *phaseManager) stepFor(phase PhaseType, role RoleType, skill SkillType) (PhaseStep, bool) {
	pc := p.phaseConfig(phase)
	if pc == nil {
		return PhaseStep{}, false
	}
	// An empty skill cannot be submitted: that is a "wake up and look" step,
	// not an action. Without this guard SkillUnspecified would match the empty
	// step exactly.
	if skill == SkillUnspecified {
		return PhaseStep{}, false
	}
	for _, step := range pc.Steps {
		if step.Skill != skill {
			continue
		}
		if step.Role == role || step.Role == RoleUnspecified {
			return step, true
		}
	}
	return PhaseStep{}, false
}

// allowedSkills returns the skills a role may use in the given phase.
func (p *phaseManager) allowedSkills(phase PhaseType, role RoleType) []SkillType {
	config := p.phaseConfig(phase)
	if config == nil {
		return []SkillType{}
	}

	skills := make([]SkillType, 0)
	for _, step := range config.Steps {
		// An empty step is "wake up and look" and has no submittable skill
		// (see PhaseStep.Skill).
		if step.Skill == SkillUnspecified {
			continue
		}
		// UNSPECIFIED means every role qualifies.
		if step.Role == role || step.Role == RoleUnspecified {
			skills = append(skills, step.Skill)
		}
	}

	return skills
}

// nextSubPhase computes the next phase from the declarative configuration.
func (p *phaseManager) nextSubPhase(current PhaseType) PhaseType {
	// The start phase is special-cased.
	if current == PhaseStart {
		return p.config.startPhase()
	}

	// Take the next phase from the configuration.
	config := p.phaseConfig(current)
	if config != nil && config.NextPhase != PhaseUnspecified {
		return config.NextPhase
	}

	// Not in the configuration: the game ends.
	return PhaseEnd
}

// validateSkillUse checks whether a skill use is legal.
func (p *phaseManager) validateSkillUse(use *SkillUse, state *gameState) error {
	// Does the player exist?
	player, ok := state.getPlayer(use.PlayerID)
	if !ok {
		return ErrPlayerNotFound
	}

	// Who may act: two layers, lined up item for item with actorsForStep --
	// if the two disagree you get the self-contradiction of "the kernel
	// accepted his submission while telling everyone else he should not be
	// acting".
	//
	//	named by the rules   whoever is on the list; aliveness is the rules' business
	//	default              whoever is alive
	//
	// The phase a detour leads to goes through the first layer: on entering
	// that phase the player has already been written onto the list (see
	// gameState.nameDetourActor). That used to be a separate first layer
	// answering the same question as naming, with a nearly word-for-word
	// identical implementation -- one concept, two implementations.
	//
	// Aliveness is therefore the **default** qualification to act, not the
	// law. Only the trigger path used to be able to step over it -- one
	// kernel letting its own mechanism move the dead while forbidding the
	// rules' mechanism from doing the same is the kernel deciding "may the
	// dead act" on the rules' behalf. What that blocks is real play: the dead
	// in Blood on the Clocktower keep a ghost vote, and werewolf has a
	// last-words phase.
	switch named, hasNamed := state.actorsFor(state.Phase); {
	case hasNamed:
		if !contains(named, use.PlayerID) {
			return ErrSkillNotAllowed
		}
	case !player.Alive:
		return ErrPlayerDead
	}

	// Is the skill allowed in this phase, and what is its declaration?
	step, allowed := p.stepFor(state.Phase, player.Role, use.Skill)
	if !allowed {
		return ErrSkillNotAllowed
	}

	// Are the targets valid? A multi-target skill is checked one by one -- if
	// a single invalid target is mixed into one submission, the whole
	// submission should be rejected rather than silently keeping the valid
	// ones.
	for _, id := range use.Targets {
		if id == "" {
			continue
		}
		target, ok := state.getPlayer(id)
		if !ok {
			return ErrTargetNotFound
		}
		// Whether an eliminated player may be targeted is declared by the
		// step; the kernel recognises no specific skill.
		if !target.Alive && !step.AllowDeadTarget {
			return ErrTargetDead
		}
	}

	return nil
}

// phaseTree is the compound-phase hierarchy, resolved once at construction.
//
// It is a **copy** of what the configuration declared, not a window onto it.
// Config is a struct the caller keeps a pointer to, and its Phases map stays
// reachable after the engine is built; reading the hierarchy back out of it
// on every transition would mean the shape of the machine could change under
// a running game -- and would race with any caller that edited it.
type phaseTree struct {
	parent  map[PhaseType]PhaseType
	onEnter map[PhaseType][]PhaseAction
	onExit  map[PhaseType][]PhaseAction
}

// newPhaseTree freezes the hierarchy and the entry/exit actions.
func newPhaseTree(c *Config) *phaseTree {
	t := &phaseTree{
		parent:  make(map[PhaseType]PhaseType),
		onEnter: make(map[PhaseType][]PhaseAction),
		onExit:  make(map[PhaseType][]PhaseAction),
	}
	if c == nil {
		return t
	}
	for phase, pc := range c.Phases {
		if pc == nil {
			continue
		}
		if pc.Parent != PhaseUnspecified {
			t.parent[phase] = pc.Parent
		}
		if len(pc.OnEnter) > 0 {
			t.onEnter[phase] = append([]PhaseAction(nil), pc.OnEnter...)
		}
		if len(pc.OnExit) > 0 {
			t.onExit[phase] = append([]PhaseAction(nil), pc.OnExit...)
		}
	}
	return t
}

// newFlatPhaseTree builds a hierarchy from one explicit chain, for a board
// laid out by hand (see Board.Ancestry).
func newFlatPhaseTree(phase PhaseType, ancestry []PhaseType) *phaseTree {
	t := &phaseTree{
		parent:  make(map[PhaseType]PhaseType, len(ancestry)),
		onEnter: map[PhaseType][]PhaseAction{},
		onExit:  map[PhaseType][]PhaseAction{},
	}
	at := phase
	for _, up := range ancestry {
		if at == PhaseUnspecified || up == PhaseUnspecified {
			break
		}
		t.parent[at] = up
		at = up
	}
	return t
}

// isComposite reports whether a phase is a group rather than a stop.
//
// It is derived, not declared: a phase is compound exactly when some other
// phase calls it its parent. One fact, one source -- a separate boolean could
// disagree with the parent links.
func (t *phaseTree) isComposite(phase PhaseType) bool {
	for _, parent := range t.parent {
		if parent == phase {
			return true
		}
	}
	return false
}

// pathOf lists a phase and every compound phase it sits inside, innermost
// first.
//
// The walk is bounded by the number of known parents: Validate rejects a
// cycle, and this must not hang even if it is ever reached with a
// configuration that skipped validation.
func (t *phaseTree) pathOf(phase PhaseType) []PhaseType {
	if phase == PhaseUnspecified {
		return nil
	}
	out := []PhaseType{phase}
	seen := map[PhaseType]bool{phase: true}
	for at, ok := t.parent[phase]; ok && !seen[at]; at, ok = t.parent[at] {
		seen[at] = true
		out = append(out, at)
	}
	return out
}

// contains reports whether inner is outer, or sits anywhere inside it.
//
// This is what GameView.InPhase asks: a SpeechProvider wants "is it night",
// not "is the phase one of these four".
func (t *phaseTree) contains(outer, inner PhaseType) bool {
	if outer == PhaseUnspecified {
		return false
	}
	for _, p := range t.pathOf(inner) {
		if p == outer {
			return true
		}
	}
	return false
}

// transitionSets works out which phases a move actually leaves and which it
// actually enters.
//
// The rule is the one statecharts settled on: strip the compound phases the
// two ends **share**, and what is left is really being left and really being
// entered. Moving between two phases of the same night does not leave the
// night; moving out of the last one does.
//
//	NIGHT_GUARD -> NIGHT_WOLF   exit [NIGHT_GUARD]         enter [NIGHT_WOLF]
//	NIGHT_SEER  -> DAY          exit [NIGHT_SEER, NIGHT]   enter [DAY]
//	VOTE        -> NIGHT_GUARD  exit [VOTE]                enter [NIGHT, NIGHT_GUARD]
//
// Only **strict** ancestors are stripped, so a phase whose exit loops back to
// itself still leaves and re-enters: the README's single-phase cycle depends
// on it, and treating that as "no movement" would mean its round never ended.
//
// Exits come innermost first and entries outermost first, which is the order
// the actions have to run in for a group's setup to be in place before its
// members'.
func (t *phaseTree) transitionSets(from, to PhaseType) (exiting, entering []PhaseType) {
	fromPath, toPath := t.pathOf(from), t.pathOf(to)

	// strictAncestors drops the phase itself and keeps the groups above it.
	strictAncestors := func(path []PhaseType) []PhaseType {
		if len(path) == 0 {
			return nil
		}
		return path[1:]
	}

	above := make(map[PhaseType]bool, len(fromPath))
	for _, p := range strictAncestors(fromPath) {
		above[p] = true
	}
	common := make(map[PhaseType]bool, len(toPath))
	for _, p := range strictAncestors(toPath) {
		if above[p] {
			common[p] = true
		}
	}

	for _, p := range fromPath {
		if !common[p] {
			exiting = append(exiting, p)
		}
	}
	for i := len(toPath) - 1; i >= 0; i-- {
		if !common[toPath[i]] {
			entering = append(entering, toPath[i])
		}
	}
	return exiting, entering
}
