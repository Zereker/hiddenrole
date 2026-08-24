package hiddenrole

import (
	"sort"
)

// SnapshotVersion is the version of the board section's format.
//
// It is bumped on every change to that structure that is not backwards
// compatible. RestoreEngine will not read an unrecognised board, so old data
// is never poured through a new structure into something that looks fine and
// is in fact scrambled.
//
// **A bump is no longer a cliff.** It used to be: the board was the only
// thing that could be written down, so the day this number moved, every
// saved game became unreadable -- and it has moved thirteen times. A
// snapshot now carries the log that produced it, and a board this version
// cannot read is rebuilt by replaying that log instead (see RestoreEngine).
// The version stops being "which saves we abandon" and becomes "which of the
// two paths we can take".
const SnapshotVersion = 14

// Snapshot is the engine's complete serialisable state.
//
// The snapshot types and the engine's internal types are deliberately two
// separate sets: the internal ones evolve with refactoring, while a snapshot
// is a format written to storage and its field names must stay stable. The
// conversion between them is all in this file, so adding or removing a field
// produces an explicit compile error here rather than silently losing data.
//
// A snapshot does **not** contain the Config, the Logger, or the callbacks:
// the caller supplies those on restore, and the caller should own the
// versioning of the rules configuration itself.
//
// Enums serialise by **name** ("NIGHT_GUARD", not 21). A save file is meant
// to be read by people and possibly by other languages, and numbers do not
// line up.
//
// Since v10 the types themselves guarantee this: an enum is a string
// underneath, with no number-to-name translation layer left. A third-party
// custom value used to have no name and was written as a number
// (`"role":1000`), and is now a name like any built-in (`"role":"WOLF_KING"`)
// -- which is the entire difference between v9 and v10, and the reason it
// needed a version bump.
type Snapshot struct {
	Version int `json:"version"`

	// Log is the durable record the board below was produced by.
	//
	// It is what makes the rest of this structure a **cache**: everything
	// after this field can be recomputed by replaying it, which is what
	// RestoreEngine falls back on when Version is one it cannot read. Drop it
	// and the snapshot still restores today, but it loses both its history
	// and its way through the next format change -- so drop it only if you
	// are storing the log separately.
	//
	// It carries its own version, because the two move on different beats: a
	// log records what happened and changes shape almost never, while the
	// board gains and loses fields with every refactor.
	Log *GameLog `json:"log,omitempty"`

	Phase PhaseType `json:"phase"`
	Round int       `json:"round"`

	// Vars is state that lives for the whole game and belongs to no player.
	Vars map[string]string `json:"vars,omitempty"`

	// Actors are the per-phase actors the rules named. Such a list is often
	// computed in an earlier phase (the missions package picks the team
	// during nomination), so it has to travel with the snapshot, or a game
	// restored between nomination and the mission would lose its team.
	Actors map[PhaseType][]string `json:"actors,omitempty"`

	// Winner is who won this game, empty while it is undecided.
	//
	// It cannot be derived from anything else: who won was settled by the
	// VictoryChecker **at the moment the game ended** and does not change
	// afterwards, and a restored engine does not run the check again. Miss it
	// and a finished game restores as Over=true with an empty Winner --
	// Status claims its four fields come from one instant, and on this path
	// they would not line up.
	Winner Camp `json:"winner,omitempty"`

	Players      []PlayerSnapshot   `json:"players"`
	RoundContext RoundCtxSnapshot   `json:"round_context"`
	PendingUses  []SkillUseSnapshot `json:"pending_uses"`
}

// PlayerSnapshot is one player's snapshot.
type PlayerSnapshot struct {
	ID    string   `json:"id"`
	Role  RoleType `json:"role"`
	Alive bool     `json:"alive"`

	// RoundVars are this player's markers for the current round, cleared
	// every round. Who was guarded, healed or poisoned tonight all live here
	// -- they used to be three []string fields on RoundCtxSnapshot, and since
	// v8 they are folded into the player, on the same footing as the markers
	// a rules package defines for itself.
	RoundVars map[string]string `json:"round_vars,omitempty"`

	// Vars is the role's private state (werewolf's witch potions are in
	// here) -- they used to be two named bool fields, and since v7 they are
	// folded into Vars, on the same footing as a third-party role. Storing
	// this is what makes the whole mechanism work: without it a role's state
	// could only hide inside its Resolver, which is the very problem being
	// solved.
	Vars map[string]string `json:"vars,omitempty"`
}

// RoundCtxSnapshot is the round context's snapshot.
type RoundCtxSnapshot struct {
	Detours []DetourSnapshot `json:"detours,omitempty"`

	// Vars is round-scoped custom state, including a third-party role's.
	Vars map[string]string `json:"vars,omitempty"`
}

// DetourSnapshot is one pending detour.
type DetourSnapshot struct {
	PlayerID string    `json:"player_id"`
	Phase    PhaseType `json:"phase"`
}

// SkillUseSnapshot is a skill submitted but not yet resolved.
type SkillUseSnapshot struct {
	PlayerID string    `json:"player_id"`
	Skill    SkillType `json:"skill"`
	Targets  []string  `json:"targets,omitempty"`
	Phase    PhaseType `json:"phase"`
	Round    int       `json:"round"`
}

// Snapshot exports the engine's current state.
//
// The returned snapshot is a deep copy: it is safe to serialise, pass across
// goroutines, or hold onto indefinitely, and later play does not affect it.
//
// It includes the skills submitted in the current phase but not yet resolved,
// so a game can be saved mid-phase, restored, keep collecting skills, and
// then resolve with EndPhase.
func (e *Engine) Snapshot() *Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snap := &Snapshot{
		Version:      SnapshotVersion,
		Log:          e.logLocked(),
		Phase:        e.state.Phase,
		Round:        e.state.Round,
		Vars:         copyVars(e.state.Vars),
		Actors:       copyActors(e.state.Actors),
		Winner:       e.winner,
		Players:      e.state.snapshotPlayers(),
		RoundContext: e.state.snapshotRoundCtx(),
		PendingUses:  make([]SkillUseSnapshot, 0, len(e.pendingUses)),
	}

	for _, use := range e.pendingUses {
		snap.PendingUses = append(snap.PendingUses, SkillUseSnapshot{
			PlayerID: use.PlayerID,
			Skill:    use.Skill,
			Targets:  append([]string(nil), use.Targets...),
			Phase:    use.Phase,
			Round:    use.Round,
		})
	}

	return snap
}

// RestoreEngine rebuilds an engine from a snapshot.
//
// **The rules configuration supplied on restore must match the one in force
// when the snapshot was taken** -- a snapshot records the board, not the
// rules, and restoring under a different configuration gives you a game whose
// rules changed halfway through.
//
// Resolvers for custom roles must be passed through opts (WithResolver).
// Omitting one makes that phase's skills be silently dropped, so resolver
// validation runs here and a missing one is an outright error.
//
// # Two paths
//
// A snapshot is a board plus the log that produced it, and the log is the
// authoritative half:
//
//	Version recognised    read the board directly, and adopt the log as history
//	Version not recognised   ignore the board and replay the log instead
//
// The second path is what a format change costs now. Before the log was
// durable there was no second path: an unrecognised version was rejected, and
// since the number had moved thirteen times, thirteen sets of saved games had
// been abandoned. A board is derived data, and derived data does not need a
// migration -- it needs to be thrown away and recomputed.
//
// The second path costs one thing: **skills submitted in the current phase
// and not yet resolved are lost.** They have not become effects, so they were
// never in the log, and only the board section holds them. A game rebuilt
// this way resumes at the start of its current phase rather than mid-phase,
// which is the right trade against not being restorable at all -- but it is a
// difference, and a caller upgrading across a format change should expect it.
//
// Errors: a nil snapshot; a version this build cannot read *and* no usable
// log to rebuild from; an empty or duplicate player ID; a phase not present
// in the config; a phase with no resolver.
func RestoreEngine(config *Config, snap *Snapshot, opts ...EngineOption) (*Engine, error) {
	if snap == nil {
		return nil, ErrNilSnapshot
	}

	// Decide which of the two paths is even available before building
	// anything. A snapshot this build can neither read nor rebuild is a
	// cheaper and more specific complaint than whatever the configuration
	// turns out to be missing, and reporting it first is what tells the
	// caller their stored data is the problem.
	rebuild := snap.Version != SnapshotVersion
	if rebuild && (snap.Log == nil || snap.Log.Version != LogVersion) {
		return nil, WrapError(CodeInvalidSnapshot,
			"unsupported snapshot version %d (expected %d), and no log to rebuild from",
			snap.Version, SnapshotVersion)
	}

	engine, err := NewEngine(config, opts...)
	if err != nil {
		return nil, err
	}

	// Same check as Start: a phase with no resolver silently drops the skills
	// it receives, a failure that is nearly impossible to locate mid-game and
	// so has to be caught before the engine is handed over.
	if err := engine.phase.validateResolvers(); err != nil {
		return nil, err
	}

	// The engine has not been handed to the caller yet, but take the lock all
	// the same: every access to state happening under the engine lock is this
	// concurrency model's one premise, and it gets no exceptions.
	engine.mu.Lock()
	defer engine.mu.Unlock()

	if rebuild {
		engine.logger.Warn("snapshot board is of an unreadable version, rebuilding from the log",
			logField("snapshot_version", snap.Version),
			logField("supported_version", SnapshotVersion))
		if err := engine.replayLocked(snap.Log.Effects); err != nil {
			return nil, err
		}
		return engine, nil
	}

	if err := engine.restoreBoard(snap); err != nil {
		return nil, err
	}
	return engine, nil
}

// restoreBoard reads the board section straight in, and adopts the log that
// came with it as this engine's history. The caller must hold e.mu.
//
// Adopting rather than replaying is the whole point of keeping a board: the
// two are equivalent, and reading the board is what makes a restore cheap.
// Invariant B in enginetest is what licenses the shortcut -- it asserts on
// every step of every random game that the two paths land on the same board.
func (e *Engine) restoreBoard(snap *Snapshot) error {
	if err := e.restorePhase(snap.Phase); err != nil {
		return err
	}
	if err := e.restorePlayers(snap.Players); err != nil {
		return err
	}
	if err := e.restorePendingUses(snap.PendingUses); err != nil {
		return err
	}

	e.state.Vars = copyVars(snap.Vars)
	e.state.Actors = copyActors(snap.Actors)
	e.winner = snap.Winner
	e.state.restoreProgress(snap.Phase, snap.Round, snap.RoundContext)

	if snap.Log != nil {
		e.recordEffects(snap.Log.Effects...)
	}
	return nil
}

// restorePhase checks that the snapshot's phase exists in the configuration.
//
// If it does not, the restored engine cannot advance. START and END are the
// two ends of the flow, do not appear in the phase configuration, and are
// allowed through separately.
func (e *Engine) restorePhase(phase PhaseType) error {
	if phase == PhaseStart || phase == PhaseEnd {
		return nil
	}
	if e.phase.phaseConfig(phase) == nil {
		return WrapError(CodeInvalidSnapshot,
			"phase %v is not present in the supplied config", phase)
	}
	return nil
}

// restorePlayers writes the players in from the snapshot.
//
// The validation matches AddPlayer's: restorePlayer deliberately does not go
// through AddPlayer (it has to restore aliveness and Vars verbatim), but that
// is no reason to let through a role AddPlayer would reject.
func (e *Engine) restorePlayers(players []PlayerSnapshot) error {
	for _, p := range players {
		if p.ID == "" {
			return ErrInvalidPlayerID
		}
		if p.Role == RoleUnspecified || p.Role == RoleSystem {
			return WrapError(CodeInvalidRole,
				"role %v cannot be assigned to a player", p.Role)
		}
		if _, exists := e.state.getPlayer(p.ID); exists {
			return WrapError(CodeInvalidSnapshot,
				"duplicate player %q in snapshot", p.ID)
		}
		e.state.restorePlayer(p)
	}
	return nil
}

// restorePendingUses restores the skills submitted but not yet resolved.
// Every player and target they reference must exist, or they would be
// silently dropped at resolution time.
func (e *Engine) restorePendingUses(uses []SkillUseSnapshot) error {
	for _, u := range uses {
		if _, ok := e.state.getPlayer(u.PlayerID); !ok {
			return WrapError(CodeInvalidSnapshot,
				"pending skill references unknown player %q", u.PlayerID)
		}
		for _, id := range u.Targets {
			if id == "" {
				continue
			}
			if _, ok := e.state.getPlayer(id); !ok {
				return WrapError(CodeInvalidSnapshot,
					"pending skill references unknown target %q", id)
			}
		}
		e.pendingUses = append(e.pendingUses, &SkillUse{
			PlayerID: u.PlayerID,
			Skill:    u.Skill,
			Targets:  append([]string(nil), u.Targets...),
			Phase:    u.Phase,
			Round:    u.Round,
		})
	}
	return nil
}

// ==================== Conversions on the state side ====================

// snapshotPlayers exports the player list, sorted by ID so that snapshots are
// comparable.
func (s *gameState) snapshotPlayers() []PlayerSnapshot {
	out := make([]PlayerSnapshot, 0, len(s.players))
	for _, p := range s.players {
		out = append(out, PlayerSnapshot{
			ID:        p.ID,
			Role:      p.Role,
			Alive:     p.Alive,
			RoundVars: copyVars(p.RoundVars),
			Vars:      copyVars(p.Vars),
		})
	}
	sortPlayerSnapshots(out)
	return out
}

// snapshotRoundCtx exports the round context.
func (s *gameState) snapshotRoundCtx() RoundCtxSnapshot {
	if s.RoundCtx == nil {
		return RoundCtxSnapshot{}
	}

	return RoundCtxSnapshot{
		Detours: snapshotTriggers(s.RoundCtx.Detours),
		Vars:    copyVars(s.RoundCtx.Vars),
	}
}

// restorePlayer writes one player in from the snapshot.
//
// It does not go through AddPlayer: a restore has to reproduce the snapshot's
// aliveness and Vars verbatim, and AddPlayer would hand out the initial state
// through RoleSetup all over again -- a spent potion would come back.
func (s *gameState) restorePlayer(p PlayerSnapshot) {
	s.players[p.ID] = &playerState{
		ID:        p.ID,
		Role:      p.Role,
		Alive:     p.Alive,
		RoundVars: copyVars(p.RoundVars),
		Vars:      copyVars(p.Vars),
	}
}

// copyVars copies custom state. A snapshot is a deep copy and this is no
// exception -- otherwise the restored engine would share one map with the
// original, and changing either would change both.
func copyVars(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// restoreProgress restores the phase, the round and the round context.
func (s *gameState) restoreProgress(phase PhaseType, round int, rc RoundCtxSnapshot) {
	s.Phase = phase
	s.Round = round
	s.RoundCtx = &RoundContext{
		Detours: restoreTriggers(rc.Detours),
		Vars:    copyVars(rc.Vars),
	}
}

// ==================== Small helpers ====================

// sortedStrings sorts in place and returns, so that lists handed to the
// caller come out in a stable order.
func sortedStrings(in []string) []string {
	sort.Strings(in)
	return in
}

// sortPlayerSnapshots sorts by ID.
func sortPlayerSnapshots(ps []PlayerSnapshot) {
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
}

// snapshotTriggers exports the pending queue.
func snapshotTriggers(ts []Detour) []DetourSnapshot {
	if len(ts) == 0 {
		return nil
	}
	out := make([]DetourSnapshot, 0, len(ts))
	for _, t := range ts {
		// Written out field by field rather than converted: the two types
		// happen to have the same shape today, but a snapshot is a storage
		// format and Detour is an internal structure, and they should not be
		// tied together.
		//nolint:staticcheck // S1016: see above
		out = append(out, DetourSnapshot{PlayerID: t.PlayerID, Phase: t.Phase})
	}
	return out
}

// restoreTriggers restores the pending queue. The order is the resolution
// order, so it is not sorted.
func restoreTriggers(ts []DetourSnapshot) []Detour {
	if len(ts) == 0 {
		return nil
	}
	out := make([]Detour, 0, len(ts))
	for _, t := range ts {
		//nolint:staticcheck // S1016: as in snapshotTriggers, deliberately not a conversion
		out = append(out, Detour{PlayerID: t.PlayerID, Phase: t.Phase})
	}
	return out
}
