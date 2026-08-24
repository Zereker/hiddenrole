package hiddenrole

import (
	"sort"
	"strconv"
)

// Effect describes one state change.
//
// # Why the payload is split in two
//
// An effect has to carry a payload, and the payload has to survive being
// written down: the effect log is this engine's history, and history that
// cannot be persisted is not history, it is a debugging aid (see GameLog).
//
// It used to be one `Data map[string]interface{}`, which bought a third-party
// Resolver the freedom to attach anything at all, and cost the **entire
// event stream its durability**: the values went in as PhaseType, Camp,
// VarScope and []string, and came back out of a JSON round trip as strings
// and float64s, so every `effect.Data[k].(PhaseType)` in the replay path
// failed on any log that had ever been written down. The architecture paid
// event sourcing's full price -- the purity constraint on resolvers, the
// single write point, the determinism discipline -- and collected half its
// value.
//
// The split follows the same principle EventType already uses: **the type is
// open, the payload is closed.**
//
//	Args   the kernel's own primitives, a closed set of typed fields
//	Data   the rules' own payload, strings only -- the same choice already
//	       made for Var values, and for the same reason
//
// A rules package needing a number or a structure encodes it (strconv, or a
// JSON string). That is a real cost, paid once at the edge, in exchange for
// an effect log that round-trips byte for byte.
type Effect struct {
	Type     EventType `json:"type"`
	SourceID string    `json:"source_id,omitempty"` // where it came from (player ID)
	TargetID string    `json:"target_id,omitempty"` // what it is aimed at (player ID)
	Canceled bool      `json:"canceled,omitempty"`  // vetoed, e.g. by a protection
	Reason   string    `json:"reason,omitempty"`    // why it was vetoed

	// Data is the rules' own payload: their name for what happened, and
	// whatever detail goes with it. The kernel never reads it.
	Data map[string]string `json:"data,omitempty"`

	// Args is the payload of the kernel's own effect types. Which of its
	// fields carry meaning is decided by Type, and the constructors
	// (NewSetAliveEffect and friends) are the only supported way to fill it
	// in; nil for a rule event.
	Args *EffectArgs `json:"args,omitempty"`
}

// EffectArgs is the closed payload of the kernel's own effects.
//
// One flat struct rather than a field per effect type: the set is closed and
// small, the constructors are the only writers, and a flat shape serialises
// to JSON that a person can read. Which fields matter for which type:
//
//	SET_ALIVE      Alive
//	SET_VAR        Scope, Key, Value
//	SET_ACTORS     Phase, Actors
//	DETOUR         Phase
//	GOTO_PHASE     Phase
//	PLAYER_ADDED   Role, Vars
//	PHASE_CHANGED  Phase
//	GAME_STARTED   Phase
//	GAME_ENDED     Winner
type EffectArgs struct {
	// Phase is the phase this effect points at.
	Phase PhaseType `json:"phase,omitempty"`

	// Alive is the value SET_ALIVE writes. It carries no omitempty on
	// purpose: false is the interesting case, and omitting it would make
	// every elimination read as an empty payload.
	Alive bool `json:"alive"`

	// Scope, Key and Value are what SET_VAR writes.
	Scope VarScope `json:"scope,omitempty"`
	Key   string   `json:"key,omitempty"`
	Value string   `json:"value,omitempty"`

	// Actors is the player list SET_ACTORS names.
	Actors []string `json:"actors,omitempty"`

	// Role and Vars are the seat PLAYER_ADDED records: which role, and what
	// initial state it sat down with.
	Role RoleType          `json:"role,omitempty"`
	Vars map[string]string `json:"vars,omitempty"`

	// Winner is who won, recorded on GAME_ENDED.
	Winner Camp `json:"winner,omitempty"`
}

// clone deep-copies the payload, the slice and the map included.
func (a *EffectArgs) clone() *EffectArgs {
	if a == nil {
		return nil
	}
	c := *a
	c.Actors = append([]string(nil), a.Actors...)
	c.Vars = copyVars(a.Vars)
	return &c
}

// eventKind classifies a kernel event.
//
// "How many classes of kernel event are there" used to be a single sentence
// of comment on the kernelPrimitives table -- "they are the state machine's
// bookkeeping (whose alive bit flipped, who gained a marker)". That sentence
// was **false** for GOTO_PHASE: it has no branch in applyEffect at all and
// changes no state whatsoever. The behaviour was always right (never sent
// out), the classification was wrong, and a classification that lives only in
// a comment makes no noise when it is.
//
// The class is now a value, so all three properties can be asserted (see
// effect_test.go): a state write must actually be able to change state, and a
// control directive or replay bookkeeping entry must not move a single byte.
type eventKind uint8

const (
	// kindRuleEvent is the rules' name for something that happened (KILL,
	// SHOOT, a duel). The kernel does not recognise it, pushes it to
	// OnEvent, and lets the rules decide its audience. This is the zero
	// value: anything absent from the table below falls into this class.
	kindRuleEvent eventKind = iota

	// kindStateWrite is a state-writing primitive, with its own branch in
	// applyEffect.
	kindStateWrite

	// kindControl is a control directive. It changes no state, and only
	// affects where the kernel goes next.
	kindControl

	// kindReplay is bookkeeping for effect-log replay, written into the log
	// by the kernel itself.
	kindReplay
)

// kernelEvents lists the events the kernel recognises, and what class each
// one is.
//
// Anything absent from the table is a rule event -- the table decides, not a
// numeric range. It used to read "anything >= 100 is internal", which
// collided head-on with the convention that third-party values start at 1000:
// every event type an extension defined was judged internal, so an
// extension's events could not be sent at all.
var kernelEvents = map[EventType]eventKind{
	EventSetAlive:  kindStateWrite,
	EventSetVar:    kindStateWrite,
	EventSetActors: kindStateWrite,
	EventDetour:    kindStateWrite,

	EventGotoPhase: kindControl,

	EventPlayerAdded:  kindReplay,
	EventPhaseChanged: kindReplay,
}

// isInternalEvent reports whether an event is one of the kernel's own
// primitives.
//
// None of the three kernel classes has any business in front of a player --
// AudienceOf answers "definitely shown to nobody" for them, and that part is
// not configurable. Rule events are the opposite: the rules decide their
// audience.
func isInternalEvent(t EventType) bool {
	return kernelEvents[t] != kindRuleEvent
}

// NewDetourEffect declares "for the sake of this player, take a trip through
// that phase" (see Detour).
//
// Werewolf uses it for "the hunter shoots after being killed", but what the
// kernel recognises is neither death nor a skill -- only "who, and to which
// phase". What triggered it and what they do once there is entirely the
// rules' business. Shooting on elimination, self-detonating, flipping a card,
// any "hold on, someone still has to act" goes through here.
//
// The division of labour with NewGotoPhaseEffect: that one is a **one-off
// rewrite of the next stop**, this one **files a debt** -- victory checks and
// the round boundary all wait until the queue drains.
func NewDetourEffect(playerID string, phase PhaseType) *Effect {
	e := NewEffect(EventDetour, playerID, "")
	e.Args = &EffectArgs{Phase: phase}
	return e
}

// NewGotoPhaseEffect declares "once this phase resolves, go to that phase".
//
// It overrides the default exit in PhaseConfig.NextPhase. Phase progression
// used to be a purely static graph whose only dynamic jump was the detour
// queue -- so every conditional branch had to go through that back door,
// whose meaning is "someone's skill is pending", not "where to go next".
//
// The missions package's "go to the mission if the vote passes, back to
// nomination otherwise" is the plainest form of such a branch: the outcome is
// computed by this phase's resolution, and a static graph cannot express it.
//
// Priority: a pending detour queue > this effect > PhaseConfig.NextPhase.
// Detours come first because the queue has to drain -- victory checks and the
// round boundary are waiting on it, and jumping away mid-queue would drop a
// debt that has not been settled.
//
// When the destination is not in the configuration the kernel logs an error
// and falls back to NextPhase: one malformed effect should not bring down a
// whole game, but neither may it quietly jump somewhere nobody expected.
func NewGotoPhaseEffect(phase PhaseType) *Effect {
	e := NewEffect(EventGotoPhase, "", "")
	e.Args = &EffectArgs{Phase: phase}
	return e
}

// phaseOf reads the phase out of an effect that points at one.
func phaseOf(e *Effect) (PhaseType, bool) {
	if e.Args == nil || e.Args.Phase == PhaseUnspecified {
		return PhaseUnspecified, false
	}
	return e.Args.Phase, true
}

// NewSetAliveEffect declares "set this player's alive flag to this value".
//
// This is the engine's only life-and-death primitive. A wolf kill, a
// poisoning, an exile and a gunshot each used to be an event type that
// changed the alive flag, which wrote a werewolf rule -- "here are the ways
// to die" -- into the engine; a different ruleset (death by duel, dying of a
// broken heart) meant one more event type and one more branch.
//
// The ways to die are now named by the rules: emit an event of your own (KILL
// / SHOOT / heartbreak) as the account of what happened, and emit a SET_ALIVE
// to actually change the state. Two effects, two things -- the first for the
// audience and the effect log, the second for the state machine.
func NewSetAliveEffect(playerID string, alive bool) *Effect {
	e := NewEffect(EventSetAlive, "", playerID)
	e.Args = &EffectArgs{Alive: alive}
	return e
}

// SetsAlive reports whether this effect changes the alive flag, and to what.
//
// An extension that wants to intercept a death needs it: the idiot surviving
// an exile by flipping their card works by replacing the lethal primitive
// (see Interceptor). Intercepting the primitive rather than the word "exile"
// makes it **independent of the cause** -- one piece of code stops a wolf
// kill, a poisoning, a gunshot and any third-party ruleset's way of dying,
// because all of them end up here.
func (e *Effect) SetsAlive() (alive, ok bool) {
	if e == nil || e.Type != EventSetAlive || e.Args == nil {
		return false, false
	}
	return e.Args.Alive, true
}

// NewSetVarEffect declares "set this piece of custom state to this value", in
// the scope given by scope.
//
// The four scopes used to be four constructors, so nothing forced the 2x2
// table to be complete -- the "whole game, unowned" cell was missing for a
// long time and nobody noticed. The scope is now a parameter:
//
//	NewSetVarEffect(ScopeGame, "score", "3")              whole game, unowned
//	NewSetVarEffect(ScopeGame.Of(id), "antidote", "used") whole game, one player
//	NewSetVarEffect(ScopeRound, "kill", target)           this round, unowned
//	NewSetVarEffect(ScopeRound.Of(id), "guarded", "1")    this round, one player
//
// This is the proper way for a role to store its own state. The idiot's
// "card already flipped", the knight's "duel spent", the witch's two potions
// and the guard's protection record are all the same thing and take the same
// route. Taking it is what earns the whole apparatus for free: the state
// travels with the snapshot, the effect log can replay it, and a Resolver can
// therefore stay stateless -- which is what the Resolver interface demands.
//
// Passing an empty value deletes the entry, identically in all four scopes.
func NewSetVarEffect(scope VarScope, key, value string) *Effect {
	e := NewEffect(EventSetVar, "", scope.owner)
	e.Args = &EffectArgs{Scope: scope, Key: key, Value: value}
	return e
}

// SetsVar reports whether this effect writes a piece of custom state, and if
// so which cell, key and value.
//
// Same use as SetsAlive: an extension that wants to intercept or observe a
// class of write needs it. With the four scopes folded into one event type,
// Type alone no longer distinguishes whole-game from this-round, or owned
// from unowned -- read them from here.
func (e *Effect) SetsVar() (scope VarScope, key, value string, ok bool) {
	if e == nil || e.Type != EventSetVar || e.Args == nil {
		return VarScope{}, "", "", false
	}
	return e.Args.Scope, e.Args.Key, e.Args.Value, e.Args.Key != ""
}

// NewSetActorsEffect declares "these players may act in the given phase".
//
// The kernel's default way of deciding actors is to match PhaseStep.Role
// against a player's role -- and a role is fixed at seating time, so any set
// of actors **chosen at runtime** is inexpressible: the missions package's
// team is voted on in the previous phase, and its leader rotates by seat.
// Without this effect the rules could only let everyone submit and then throw
// away what should not count, while the kernel told unqualified players "you
// may act".
//
// Priority: a pending detour queue > this effect > PhaseStep.Role. Same
// layering as NewGotoPhaseEffect -- a default plus a runtime override.
//
// The list is normally computed in an **earlier phase**, which is why it
// names a phase rather than applying to the current one. A phase's list is
// consumed once that phase resolves: without clearing it, the next visit to
// the same phase would inherit the previous round's list.
//
// Passing an empty list is meaningful: it says "nobody can act in this
// phase", which is different from "the rules did not say".
//
// Players in the list who do not exist are ignored; the list is stored sorted
// by ID, which keeps the effect log deterministic.
func NewSetActorsEffect(phase PhaseType, playerIDs ...string) *Effect {
	e := NewEffect(EventSetActors, "", "")
	e.Args = &EffectArgs{
		Phase:  phase,
		Actors: append([]string(nil), playerIDs...),
	}
	return e
}

// actorsOf reads the phase and the list out of an effect.
func actorsOf(e *Effect) (PhaseType, []string, bool) {
	if e.Args == nil || e.Args.Phase == PhaseUnspecified {
		return PhaseUnspecified, nil, false
	}
	return e.Args.Phase, e.Args.Actors, true
}

// NewEffect builds a rule event: the rules' own name for something that
// happened.
//
// Attach detail with WithData. The kernel never reads it -- it goes to the
// audience the rules choose, and into the effect log.
func NewEffect(eventType EventType, sourceID, targetID string) *Effect {
	return &Effect{
		Type:     eventType,
		SourceID: sourceID,
		TargetID: targetID,
	}
}

// Cancel vetoes an effect.
func (e *Effect) Cancel(reason string) {
	e.Canceled = true
	e.Reason = reason
}

// WithData attaches one piece of the rules' own payload.
//
// Values are strings. That is the same choice made for Var values, for the
// same two reasons: an effect log has to survive being written down (see the
// Effect type documentation), and a payload whose type is decided at runtime
// cannot be read back without an assertion that may fail.
//
// It builds Data in place when nil, so constructing an Effect as a literal --
// the documented thing for a third-party Resolver to do -- does not run into
// an "assignment to entry in nil map" here.
func (e *Effect) WithData(key, value string) *Effect {
	if e.Data == nil {
		e.Data = make(map[string]string, 1)
	}
	e.Data[key] = value
	return e
}

// clone deep-copies one effect, the payload included.
//
// The effect log is this engine's history, and "history cannot be rewritten"
// cannot rest on documentation alone: what EndPhase returned and what
// EffectLog returned used to be the very same pointers as the engine's own
// history, so a caller changing one field in passing (or calling Cancel,
// which is exported) rewrote the history, and a replay would rebuild a
// different game from it.
//
// The copy has to reach **inside** the payload as well. It used to stop at
// the top level of Data, so a composite value -- the actor list -- stayed
// shared with the caller, and the promise held for scalar fields only.
func (e *Effect) clone() *Effect {
	if e == nil {
		return nil
	}
	c := *e
	c.Data = copyVars(e.Data)
	c.Args = e.Args.clone()
	return &c
}

// ToEvent converts an effect into an outward event.
//
// The rules' payload is carried over as it stands, and the kernel's typed
// payload is flattened alongside it under fixed keys, so that a caller
// routing GAME_ENDED can read the winner without knowing about EffectArgs.
// Canceled and Reason are carried over verbatim -- an action the rules vetoed
// that lost its marker here would reach the caller looking exactly like one
// that really happened.
func (e *Effect) ToEvent() *Event {
	event := &Event{
		Type:     e.Type,
		SourceID: e.SourceID,
		TargetID: e.TargetID,
		Data:     make(map[string]string, len(e.Data)),
		Canceled: e.Canceled,
		Reason:   e.Reason,
	}
	for k, v := range e.Data {
		event.Data[k] = v
	}
	e.Args.flattenInto(event.Data)
	return event
}

// The keys under which the kernel's typed payload appears in an outward
// Event. Fixed names: a caller reading GAME_ENDED's winner should not have to
// know how the payload is stored.
const (
	EventKeyPhase  = "phase"
	EventKeyAlive  = "alive"
	EventKeyScope  = "scope"
	EventKeyVarKey = "var_key"
	EventKeyValue  = "value"
	EventKeyActors = "actors"
	EventKeyRole   = "role"
	EventKeyWinner = "winner"
)

// flattenInto writes the typed payload into an event's string map.
//
// Only fields that carry meaning are written. Actors is joined in the order
// it is stored, which setActors keeps sorted, so the rendering is
// deterministic.
func (a *EffectArgs) flattenInto(data map[string]string) {
	if a == nil {
		return
	}
	if a.Phase != PhaseUnspecified {
		data[EventKeyPhase] = string(a.Phase)
	}
	if a.Alive {
		data[EventKeyAlive] = strconv.FormatBool(a.Alive)
	}
	if a.Key != "" {
		data[EventKeyScope] = a.Scope.String()
		data[EventKeyVarKey] = a.Key
		data[EventKeyValue] = a.Value
	}
	if len(a.Actors) > 0 {
		ids := append([]string(nil), a.Actors...)
		sort.Strings(ids)
		data[EventKeyActors] = joinIDs(ids)
	}
	if a.Role != RoleUnspecified {
		data[EventKeyRole] = string(a.Role)
	}
	if a.Winner != CampUnspecified {
		data[EventKeyWinner] = string(a.Winner)
	}
}

// joinIDs renders a player list for an outward event.
func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}
