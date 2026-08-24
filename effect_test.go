package hiddenrole

import (
	"encoding/json"
	"testing"
)

func TestNewEffect(t *testing.T) {
	effect := NewEffect(eventKill, "wolf1", "villager1")

	if effect.Type != eventKill {
		t.Errorf("Type = %v, want %v", effect.Type, eventKill)
	}
	if effect.SourceID != "wolf1" {
		t.Errorf("SourceID = %v, want wolf1", effect.SourceID)
	}
	if effect.TargetID != "villager1" {
		t.Errorf("TargetID = %v, want villager1", effect.TargetID)
	}
	// Data starts nil and is built by WithData on first use. An effect with
	// no payload serialises without an empty object in it, which matters now
	// that the log is the thing being written down.
	if effect.Data != nil {
		t.Errorf("Data = %v, want nil until something is attached", effect.Data)
	}
	if effect.Canceled {
		t.Error("a new effect should not be cancelled")
	}
}

func TestEffect_Cancel(t *testing.T) {
	effect := NewEffect(eventKill, "wolf", "victim")

	effect.Cancel("protected by guard")

	if !effect.Canceled {
		t.Error("expected Canceled=true")
	}
	if effect.Reason != "protected by guard" {
		t.Errorf("expected Reason='protected by guard', got %s", effect.Reason)
	}
}

func TestEffect_WithData(t *testing.T) {
	effect := NewEffect(eventCheck, "seer", "target")

	result := effect.WithData("camp", string(campGood))

	// Verify method chaining
	if result != effect {
		t.Error("expected WithData to return same effect")
	}

	if effect.Data["camp"] != string(campGood) {
		t.Errorf("expected camp=GOOD, got %v", effect.Data["camp"])
	}
}

func TestEffect_WithData_Multiple(t *testing.T) {
	effect := NewEffect(eventKill, "wolf", "victim")

	effect.WithData("key1", "value1").WithData("key2", "value2")

	if effect.Data["key1"] != "value1" {
		t.Errorf("expected key1=value1, got %v", effect.Data["key1"])
	}
	if effect.Data["key2"] != "value2" {
		t.Errorf("expected key2=value2, got %v", effect.Data["key2"])
	}
}

func TestEffect_ToEvent_Kill(t *testing.T) {
	effect := NewEffect(eventKill, "wolf", "victim")

	event := effect.ToEvent()

	if event.Type != eventKill {
		t.Errorf("expected KILL, got %v", event.Type)
	}
	if event.SourceID != "wolf" {
		t.Errorf("expected SourceId=wolf, got %s", event.SourceID)
	}
	if event.TargetID != "victim" {
		t.Errorf("expected TargetId=victim, got %s", event.TargetID)
	}
}

func TestEffect_ToEvent_Poison(t *testing.T) {
	effect := NewEffect(eventPoison, "witch", "victim")

	event := effect.ToEvent()

	if event.Type != eventPoison {
		t.Errorf("expected POISON, got %v", event.Type)
	}
}

func TestEffect_ToEvent_Protect(t *testing.T) {
	effect := NewEffect(eventProtect, "guard", "target")

	event := effect.ToEvent()

	if event.Type != eventProtect {
		t.Errorf("expected PROTECT, got %v", event.Type)
	}
}

func TestEffect_ToEvent_Save(t *testing.T) {
	effect := NewEffect(eventSave, "witch", "victim")

	event := effect.ToEvent()

	if event.Type != eventSave {
		t.Errorf("expected SAVE, got %v", event.Type)
	}
}

func TestEffect_ToEvent_Check(t *testing.T) {
	effect := NewEffect(eventCheck, "seer", "target")

	event := effect.ToEvent()

	if event.Type != eventCheck {
		t.Errorf("expected CHECK, got %v", event.Type)
	}
}

func TestEffect_ToEvent_Eliminate(t *testing.T) {
	effect := NewEffect(eventEliminate, "", "target")

	event := effect.ToEvent()

	if event.Type != eventEliminate {
		t.Errorf("expected ELIMINATE, got %v", event.Type)
	}
}

func TestEffect_ToEvent_Unspecified(t *testing.T) {
	effect := NewEffect(EventUnspecified, "", "")

	event := effect.ToEvent()

	if event.Type != EventUnspecified {
		t.Errorf("expected UNSPECIFIED, got %v", event.Type)
	}
}

func TestEventType_AllTypes(t *testing.T) {
	types := []EventType{
		EventUnspecified,
		EventGameStarted,
		EventGameEnded,
		eventKill,
		eventProtect,
		eventSave,
		eventPoison,
		eventCheck,
		eventEliminate,
	}

	// Verify all types are distinct
	seen := make(map[EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate EventType: %v", et)
		}
		seen[et] = true
	}
}

func TestEffect_ToEvent_WithData(t *testing.T) {
	effect := NewEffect(eventCheck, "seer", "target").
		WithData("camp", string(campGood)).
		WithData("is_good", "true").
		WithData("votes", "5")

	event := effect.ToEvent()

	if event.Data == nil {
		t.Fatal("expected Data to be initialized")
	}
	for key, want := range map[string]string{"camp": "GOOD", "is_good": "true", "votes": "5"} {
		if got := event.Data[key]; got != want {
			t.Errorf("Data[%q] = %q, want %q", key, got, want)
		}
	}
}

// TestEffect_ToEvent_CarriesKernelPayload: a caller routing GAME_ENDED must
// be able to read the winner without knowing that EffectArgs exists.
//
// The kernel's typed payload and the rules' free-form one are stored apart,
// and the split must not leak into what a handler receives -- an event is the
// shape the outside world reads, and it has one map.
func TestEffect_ToEvent_CarriesKernelPayload(t *testing.T) {
	event := newGameEndedEffect(campGood).ToEvent()
	if got := event.Data[EventKeyWinner]; got != string(campGood) {
		t.Errorf("winner in the event = %q, want %q", got, campGood)
	}

	event = NewSetActorsEffect(phaseVote, "p2", "p1").ToEvent()
	if got := event.Data[EventKeyActors]; got != "p1,p2" {
		t.Errorf("actors in the event = %q, want p1,p2 (sorted)", got)
	}
}

// TestEffect_SurvivesBeingWrittenDown is the property the whole payload split
// exists for.
//
// The effect log is the durable record, and a record that cannot be written
// down and read back is not a record. Every kernel primitive is round-tripped
// through JSON here and has to come back **identical** -- byte for byte, and
// through the typed accessors, since those are what the replay path uses.
//
// This is the test that could not have been written before: the payload was
// a map[string]interface{}, so a PhaseType went in and a string came back,
// and every `Data[k].(PhaseType)` in the replay path failed on any log that
// had ever touched storage.
func TestEffect_SurvivesBeingWrittenDown(t *testing.T) {
	cases := []struct {
		name   string
		effect *Effect
		check  func(t *testing.T, back *Effect)
	}{
		{
			name:   "SET_ALIVE false",
			effect: NewSetAliveEffect("p1", false),
			check: func(t *testing.T, back *Effect) {
				alive, ok := back.SetsAlive()
				if !ok || alive {
					t.Errorf("SetsAlive() = (%v, %v), want (false, true)", alive, ok)
				}
			},
		},
		{
			name:   "SET_VAR in a per-player round scope",
			effect: NewSetVarEffect(ScopeRound.Of("p1"), "guarded", VarPresent),
			check: func(t *testing.T, back *Effect) {
				scope, key, value, ok := back.SetsVar()
				if !ok || key != "guarded" || value != VarPresent {
					t.Fatalf("SetsVar() = (%v, %q, %q, %v)", scope, key, value, ok)
				}
				// The cell matters most: the wrong one silently files a
				// round-scoped write under whole-game storage.
				if scope != ScopeRound.Of("p1") {
					t.Errorf("scope = %v, want %v", scope, ScopeRound.Of("p1"))
				}
			},
		},
		{
			name:   "SET_ACTORS",
			effect: NewSetActorsEffect(phaseVote, "p1", "p2"),
			check: func(t *testing.T, back *Effect) {
				phase, ids, ok := actorsOf(back)
				if !ok || phase != phaseVote || len(ids) != 2 || ids[0] != "p1" || ids[1] != "p2" {
					t.Errorf("actorsOf() = (%v, %v, %v)", phase, ids, ok)
				}
			},
		},
		{
			name:   "DETOUR",
			effect: NewDetourEffect("p1", phaseNightHunter),
			check: func(t *testing.T, back *Effect) {
				if phase, ok := phaseOf(back); !ok || phase != phaseNightHunter {
					t.Errorf("phaseOf() = (%v, %v)", phase, ok)
				}
			},
		},
		{
			name:   "GAME_ENDED",
			effect: newGameEndedEffect(campGood),
			check: func(t *testing.T, back *Effect) {
				if back.Args == nil || back.Args.Winner != campGood {
					t.Errorf("winner = %v, want %v", back.Args, campGood)
				}
			},
		},
		{
			name:   "PLAYER_ADDED with seat state",
			effect: newPlayerAddedEffect("p1", roleWitch, map[string]string{"antidote": "1"}),
			check: func(t *testing.T, back *Effect) {
				if back.Args == nil || back.Args.Role != roleWitch || back.Args.Vars["antidote"] != "1" {
					t.Errorf("seat state did not survive: %+v", back.Args)
				}
			},
		},
		{
			name:   "a rule event with its own payload",
			effect: NewEffect(eventKill, "wolf", "victim").WithData("reason", "night"),
			check: func(t *testing.T, back *Effect) {
				if back.Data["reason"] != "night" {
					t.Errorf("rule payload did not survive: %v", back.Data)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.effect)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var back Effect
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			tc.check(t, &back)

			again, err := json.Marshal(&back)
			if err != nil {
				t.Fatalf("Marshal again: %v", err)
			}
			if string(again) != string(raw) {
				t.Errorf("a second round trip differs:\n  first  %s\n  second %s", raw, again)
			}
		})
	}
}

// TestEventType_KernelPrimitivesAreTheOnlyInternalOnes: only the kernel's own
// state primitives count as internal events.
//
// This decision used to be made by numeric range: ">= 100 is internal". That
// collided head-on with the other convention, "third-party values start at
// 1000" -- every event type a third party defined was judged internal, so
// things that should be visible to the whole table (the idiot flipping a
// card, the wolf king self-detonating) could not be emitted by an extension
// at all.
//
// With the enums as strings there are no ranges any more, and what decides is
// the kernel's own table: what is in it is bookkeeping, what is outside it is
// the rules' event and gets pushed to OnEvent.
func TestEventType_KernelPrimitivesAreTheOnlyInternalOnes(t *testing.T) {
	cases := []struct {
		typ      EventType
		internal bool
		why      string
	}{
		{eventKill, false, "the rules' name for something that happened"},
		{eventVoteTied, false, "the rules' name for something that happened"},
		{EventSetVar, true, "a kernel state primitive"},
		{EventSetAlive, true, "a kernel state primitive"},
		{EventPhaseChanged, true, "kernel bookkeeping"},
		{EventType("IDIOT_REVEALED"), false, "a third party's event"},
		{EventType("SET_ALIVE_BUT_NOT_REALLY"), false, "a lookalike name does not count; the table decides, not a prefix"},
	}
	for _, c := range cases {
		if got := isInternalEvent(c.typ); got != c.internal {
			t.Errorf("isInternalEvent(%v) = %v, want %v (%s)", c.typ, got, c.internal, c.why)
		}
	}
}

// TestAudienceOf_CustomEventIsUnknownNotHidden: the answer for a custom event
// is "don't know", not "show it to nobody".
//
// The two must stay distinguishable: the former asks the caller to route it
// themselves, the latter is the engine's definite verdict. A custom event
// used to land in the latter -- because a number >= 100 was taken as
// internal.
func TestAudienceOf_CustomEventIsUnknownNotHidden(t *testing.T) {
	e := newViewGame(t)

	custom := NewEffect(EventType("CUSTOM_EVENT"), "s", "v1")
	audience, known := e.AudienceOf(custom.ToEvent())
	if known {
		t.Error("the engine should not claim to recognise a third party's event type")
	}
	if len(audience) != 0 {
		t.Errorf("an unrecognised type should yield no audience, got %v", audience)
	}

	// Control: the engine's own internal events are a definite "shown to
	// nobody".
	internal := NewSetAliveEffect("v1", false)
	if _, known := e.AudienceOf(internal.ToEvent()); !known {
		t.Error("the engine should definitively rule that its internal events are not sent out")
	}
}

// TestCustomEventReachesOnEvent: a third party's event really does reach
// subscribers.
func TestCustomEventReachesOnEvent(t *testing.T) {
	const customPhase = PhaseType("CUSTOM_PHASE")
	const customEvent = EventType("CUSTOM_EVENT")

	cfg := testConfig()
	cfg.Phases[customPhase] = &PhaseConfig{
		Type:      customPhase,
		NextPhase: phaseDay,
		Steps:     []PhaseStep{{Role: roleVillager, Skill: SkillSkip}},
	}
	cfg.Phases[phaseNightResolve].NextPhase = customPhase

	opts := append(withNoopResolvers(),
		WithResolver(customPhase, customEventResolver{typ: customEvent}))
	e, err := NewEngine(cfg, opts...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for id, role := range map[string]RoleType{
		"w1": roleWerewolf, "w2": roleWerewolf, "s": roleSeer,
		"v1": roleVillager, "v2": roleVillager, "v3": roleVillager,
	} {
		mustAdd(t, e, id, role)
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var seen []EventType
	e.OnEvent(func(ev *Event) { seen = append(seen, ev.Type) })

	for i := 0; e.Status().Phase != customPhase && i < 20; i++ {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase: %v", err)
		}
	}
	if _, err := e.EndPhase(); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}

	for _, typ := range seen {
		if typ == customEvent {
			return
		}
	}
	t.Errorf("a third party's event should reach OnEvent subscribers, only got %v", seen)
}

// customEventResolver emits an event of a third-party custom type.
type customEventResolver struct{ typ EventType }

func (r customEventResolver) Resolve([]*SkillUse, GameView) []*Effect {
	return []*Effect{NewEffect(r.typ, "v1", "")}
}

// TestEventKind_StateWritesActuallyWriteState: a primitive classified as
// kindStateWrite must really be able to change state.
//
// This used to be one sentence of comment on kernelPrimitives -- "they are
// the state machine's bookkeeping". That sentence was false for GOTO_PHASE:
// it has no branch in applyEffect at all and changes no state. A
// classification that lives only in a comment makes no sound when it is
// wrong, and so it stayed wrong for a long time.
//
// The class is a value now, so the property can be asserted: every
// kindStateWrite is tried against a clean state, and one that cannot change
// anything is misclassified (or the write point is missing its branch).
func TestEventKind_StateWritesActuallyWriteState(t *testing.T) {
	// One representative sample of each primitive, plus how to verify it.
	probes := map[EventType]struct {
		effect  func() *Effect
		changed func(*gameState) bool
	}{
		EventSetAlive: {
			func() *Effect { return NewSetAliveEffect("p1", false) },
			func(s *gameState) bool { p, ok := s.getPlayer("p1"); return ok && !p.Alive },
		},
		EventSetVar: {
			func() *Effect { return NewSetVarEffect(ScopeGame, "probe", "1") },
			func(s *gameState) bool { return s.varOf(ScopeGame, "probe") == "1" },
		},
		EventSetActors: {
			func() *Effect { return NewSetActorsEffect(phaseDay, "p1") },
			func(s *gameState) bool { ids, ok := s.actorsFor(phaseDay); return ok && len(ids) == 1 },
		},
		EventDetour: {
			func() *Effect { return NewDetourEffect("p1", phaseDay) },
			func(s *gameState) bool { return s.hasPendingDetour() },
		},
	}

	for typ, kind := range kernelEvents {
		if kind != kindStateWrite {
			continue
		}
		probe, ok := probes[typ]
		if !ok {
			t.Errorf("%v is classified kindStateWrite but this test has no sample for it -- "+
				"add one, or \"a state write really can write state\" is unverified for it", typ)
			continue
		}
		t.Run(string(typ), func(t *testing.T) {
			state := newState()
			mustAddTo(t, state, "p1", roleVillager)
			state.applyEffect(probe.effect())
			if !probe.changed(state) {
				t.Errorf("%v is classified kindStateWrite yet changed nothing -- "+
					"either it is misclassified, or applyEffect is missing its branch", typ)
			}
		})
	}
}

// TestEventKind_ControlAndReplayWriteNothing: a control directive or a replay
// bookkeeping entry must not move a single byte.
//
// The mirror of the previous test: GOTO_PHASE is correct precisely because it
// does **not** change state (where to go next is decided by
// calculateNextPhase reading the effect log), while PLAYER_ADDED and
// PHASE_CHANGED only mean anything on the replayEffect path. The day somebody
// gives them a branch in applyEffect, this is what goes red first.
func TestEventKind_ControlAndReplayWriteNothing(t *testing.T) {
	probes := map[EventType]*Effect{
		EventGotoPhase:    NewGotoPhaseEffect(phaseDay),
		EventPlayerAdded:  newPlayerAddedEffect("p2", roleVillager, nil),
		EventPhaseChanged: newPhaseChangedEffect(phaseDay),
	}

	for typ, kind := range kernelEvents {
		if kind == kindStateWrite {
			continue
		}
		probe, ok := probes[typ]
		if !ok {
			t.Errorf("%v is not kindStateWrite but this test has no sample for it -- add one", typ)
			continue
		}
		t.Run(string(typ), func(t *testing.T) {
			before := newState()
			mustAddTo(t, before, "p1", roleVillager)
			before.startAt(phaseNight)

			after := newState()
			mustAddTo(t, after, "p1", roleVillager)
			after.startAt(phaseNight)
			after.applyEffect(probe)

			if !sameState(before, after) {
				t.Errorf("%v is classified %v yet changed state -- applyEffect should have no branch for it", typ, kind)
			}
		})
	}
}

// sameState reports whether two states agree on the fields the write point
// can see.
func sameState(a, b *gameState) bool {
	if a.Phase != b.Phase || a.Round != b.Round || len(a.players) != len(b.players) {
		return false
	}
	if len(a.Vars) != len(b.Vars) || len(a.Actors) != len(b.Actors) {
		return false
	}
	for k, v := range a.Vars {
		if b.Vars[k] != v {
			return false
		}
	}
	for _, pa := range a.players {
		pb, ok := b.players[pa.ID]
		if !ok || pa.Alive != pb.Alive || pa.Role != pb.Role {
			return false
		}
		if len(pa.Vars) != len(pb.Vars) || len(pa.RoundVars) != len(pb.RoundVars) {
			return false
		}
	}
	if a.RoundCtx == nil || b.RoundCtx == nil {
		return a.RoundCtx == b.RoundCtx
	}
	return len(a.RoundCtx.Vars) == len(b.RoundCtx.Vars) &&
		len(a.RoundCtx.Detours) == len(b.RoundCtx.Detours)
}
