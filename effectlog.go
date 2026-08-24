package hiddenrole

// newPlayerAddedEffect records a player taking a seat, along with the role's
// initial state.
//
// It records vars rather than asking RoleSetup again during replay; see
// Engine.seatPlayer for why.
func newPlayerAddedEffect(id string, role RoleType, vars map[string]string) *Effect {
	e := NewEffect(EventPlayerAdded, "", id)
	e.Args = &EffectArgs{Role: role, Vars: copyVars(vars)}
	return e
}

// newPhaseChangedEffect records a phase transition.
func newPhaseChangedEffect(phase PhaseType) *Effect {
	e := NewEffect(EventPhaseChanged, "", "")
	e.Args = &EffectArgs{Phase: phase}
	return e
}

// newGameStartedEffect records the start of the game.
func newGameStartedEffect(phase PhaseType) *Effect {
	e := NewEffect(EventGameStarted, "", "")
	e.Args = &EffectArgs{Phase: phase}
	return e
}

// newGameEndedEffect records the end of the game and who won.
func newGameEndedEffect(winner Camp) *Effect {
	e := NewEffect(EventGameEnded, "", "")
	e.Args = &EffectArgs{Winner: winner}
	return e
}

// LogVersion is the version of the effect-log format.
//
// It moves only when the **meaning** of an existing entry changes, which is a
// far rarer event than a change to the shape of the board -- adding a state
// field does not touch it, because a log records what happened rather than
// what the board looks like. That difference is the whole reason the log,
// not the snapshot, is the durable record.
const LogVersion = 1

// GameLog is the durable record of everything that has happened in one game.
//
// **This is the source of truth.** A Snapshot is derived from it and can
// always be thrown away and rebuilt (see RestoreEngine, which does exactly
// that when it cannot read a snapshot's board). That is the ordinary shape of
// an event-sourced system, and this package did not use to have it: the log
// carried `map[string]interface{}` payloads that degraded on a JSON round
// trip, so it was explicitly documented as an in-process debugging aid and
// the snapshot was the only thing that could be written down. The
// architecture paid event sourcing's full price and collected half its value.
//
// It is plain data. json.Marshal it, put it wherever you keep things, and
// hand it back to ReplayEngine.
type GameLog struct {
	Version int `json:"version"`

	// Effects is every effect the engine has recorded, in the order they
	// happened. Append-only.
	Effects []*Effect `json:"effects"`
}

// Log exports the complete effect log as a durable record.
//
// The returned value is a deep copy and shares nothing with the engine: it is
// safe to serialise, hand to another goroutine, or hold indefinitely.
//
// # Division of labour with Snapshot
//
// The log is history and is authoritative; a snapshot is the board at one
// moment, derived from the log, and exists so that a restore does not have to
// replay from the beginning. Persist the log. Persist a snapshot too if
// replays are getting long, and treat it as a cache.
func (e *Engine) Log() *GameLog {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.logLocked()
}

// logLocked builds the durable record. The caller must hold e.mu.
func (e *Engine) logLocked() *GameLog {
	out := &GameLog{
		Version: LogVersion,
		Effects: make([]*Effect, len(e.effectLog)),
	}
	for i, ef := range e.effectLog {
		out.Effects[i] = ef.clone()
	}
	return out
}

// EffectLog returns the complete effect log since the game was created.
//
// A convenience over Log() for callers that only want to walk the entries --
// post-game analysis, "what actually happened on night three". Both return
// deep copies; use Log when you are going to write it down, because that
// carries the format version with it.
func (e *Engine) EffectLog() []*Effect {
	return e.Log().Effects
}

// ReplayEngine rebuilds an engine from a durable log.
//
// config must match the one used during recording -- a log records what
// happened, not the rules.
//
// The rebuilt engine matches the recorded one in player state, phase, round
// and history; skills submitted in the current phase and not yet resolved are
// not in the log (they have not become effects yet), so use Snapshot if you
// need those in flight.
//
// Resolvers for custom roles must be passed through opts, for the same reason
// as with RestoreEngine. Initial state need not be: it is recorded on the
// seating entry of the log (see Engine.seatPlayer).
func ReplayEngine(config *Config, log *GameLog, opts ...EngineOption) (*Engine, error) {
	if log == nil {
		return nil, WrapError(CodeInvalidEffectLog, "log must not be nil")
	}
	if log.Version != LogVersion {
		return nil, WrapError(CodeInvalidEffectLog,
			"unsupported log version %d (expected %d)", log.Version, LogVersion)
	}

	engine, err := NewEngine(config, opts...)
	if err != nil {
		return nil, err
	}

	if err := engine.phase.validateResolvers(); err != nil {
		return nil, err
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()

	if err := engine.replayLocked(log.Effects); err != nil {
		return nil, err
	}
	return engine, nil
}

// replayLocked folds a sequence of recorded effects back into the engine and
// adopts them as its history. The caller must hold e.mu.
func (e *Engine) replayLocked(effects []*Effect) error {
	for i, effect := range effects {
		if effect == nil {
			return WrapError(CodeInvalidEffectLog,
				"effect log contains a nil entry at index %d", i)
		}
		if err := e.replayEffect(effect); err != nil {
			return err
		}
	}
	e.recordEffects(effects...)
	return nil
}

// replayPhase reads the phase a bookkeeping entry points at.
func (e *Effect) replayPhase() (PhaseType, bool) { return phaseOf(e) }

// replayEffect replays one effect.
func (e *Engine) replayEffect(effect *Effect) error {
	switch effect.Type {
	case EventPlayerAdded:
		var role RoleType
		var vars map[string]string
		if effect.Args != nil {
			role, vars = effect.Args.Role, effect.Args.Vars
		}
		// Seating has to hand out the initial state too. Without this the
		// replayed witch holds no potions and the wolves belong to no camp,
		// and the divergence only surfaces when a potion is used or victory is
		// checked.
		if err := e.seatPlayer(effect.TargetID, role, vars); err != nil {
			return err
		}

	case EventGameStarted:
		phase, ok := effect.replayPhase()
		if !ok {
			return WrapError(CodeInvalidEffectLog,
				"game started effect carries no phase")
		}
		e.state.startAt(phase)

	case EventPhaseChanged:
		phase, ok := effect.replayPhase()
		if !ok {
			return WrapError(CodeInvalidEffectLog,
				"phase changed effect carries no phase")
		}
		// Leaving a phase consumes what belongs to it, exactly as normal
		// progression does. Without this the replayed engine carries a detour
		// that should have been consumed and diverges from the original on the
		// very next step.
		//
		// The transition itself goes through the same enterPhase path as
		// normal progression, so the entry and exit actions of every phase on
		// the way -- compound phases included -- fire identically.
		e.state.leavePhase()
		e.transition(phase)

	case EventGameEnded:
		// Ending also leaves the current phase -- on the normal path
		// leavePhase runs **before** the victory check, whether the game ends
		// or not. Miss it and the replayed engine carries the last phase's
		// actor list and an unconsumed detour, and diverges from the original.
		e.state.leavePhase()
		e.transition(PhaseEnd)
		// The winner travels in the log. Who won was decided by the
		// VictoryChecker at the moment the game ended, and replay does not run
		// the check again -- without reading it the replayed engine has
		// Over=true and an empty Winner, and diverges from the original.
		if effect.Args != nil {
			e.winner = effect.Args.Winner
		}

	default:
		e.state.applyEffect(effect)
	}
	return nil
}

// recordEffects appends a batch of effects to the history.
//
// It stores copies: the same effects are returned verbatim to EndPhase's
// caller, and were they to share pointers, the caller changing one field
// would change the engine's history. This is the effect log's single write
// point, mirroring applyEffect as the single write point for state.
func (e *Engine) recordEffects(effects ...*Effect) {
	for _, ef := range effects {
		e.effectLog = append(e.effectLog, ef.clone())
	}
}
