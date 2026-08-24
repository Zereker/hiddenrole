package hiddenrole

import (
	"encoding/json"
	"testing"
)

// The log is the durable record and the board derived from it is a cache.
// These tests are about that relationship: the log has to survive storage,
// and the board has to be reconstructible from it when it cannot be read.

// logFixture plays a few phases and returns an engine with real history.
func logFixture(t *testing.T) (*Engine, *Config) {
	t.Helper()
	cfg := testConfig()
	e, err := NewEngine(cfg, withNoopResolvers()...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	mustAdd(t, e, "g1", roleGuard)
	mustAdd(t, e, "w1", roleWerewolf)
	mustAdd(t, e, "wi", roleWitch)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.SubmitSkillUse(&SkillUse{PlayerID: "g1", Skill: skillProtect, Targets: []string{"w1"}}); err != nil {
		t.Fatalf("SubmitSkillUse: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := e.EndPhase(); err != nil {
			t.Fatalf("EndPhase %d: %v", i, err)
		}
	}
	e.Apply(
		NewSetVarEffect(ScopeGame, "score", "3"),
		NewSetVarEffect(ScopeRound.Of("w1"), "guarded", VarPresent),
		NewSetActorsEffect(phaseVote, "g1", "wi"),
		NewSetAliveEffect("wi", false),
	)
	return e, cfg
}

// TestLog_SurvivesStorage is the property everything else here depends on.
//
// It is the test that could not have been written before: the log carried
// interface{} payloads, so writing it down and reading it back turned a
// PhaseType into a string, and the replay path's type assertions failed on
// any log that had ever touched storage. The package documented its way
// around it -- "designed for in-process replay, not as a storage format" --
// which is the same as saying the history was not durable.
func TestLog_SurvivesStorage(t *testing.T) {
	e, cfg := logFixture(t)

	raw, err := json.Marshal(e.Log())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back GameLog
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	replayed, err := ReplayEngine(cfg, &back, withNoopResolvers()...)
	if err != nil {
		t.Fatalf("ReplayEngine from stored log: %v", err)
	}

	want, _ := json.Marshal(e.Snapshot())
	got, _ := json.Marshal(replayed.Snapshot())
	if string(got) != string(want) {
		t.Errorf("a log that went through storage rebuilt a different board:\n  want %s\n  got  %s", want, got)
	}
}

// TestSnapshot_CarriesItsLog: a snapshot without the log that produced it has
// no history and no way through the next format change.
func TestSnapshot_CarriesItsLog(t *testing.T) {
	e, _ := logFixture(t)
	snap := e.Snapshot()

	if snap.Log == nil {
		t.Fatal("a snapshot should carry the log that produced it")
	}
	if got, want := len(snap.Log.Effects), len(e.EffectLog()); got != want {
		t.Errorf("the snapshot carries %d entries, the engine has %d", got, want)
	}
	if snap.Log.Version != LogVersion {
		t.Errorf("log version = %d, want %d", snap.Log.Version, LogVersion)
	}
}

// TestRestore_KeepsTheHistory: a restored engine used to start with an empty
// history -- four entries went in and zero came out.
//
// The moment a real server needs an audit trail is after a restart, a
// migration or a crash, which was exactly the moment it was empty.
func TestRestore_KeepsTheHistory(t *testing.T) {
	e, cfg := logFixture(t)
	before := len(e.EffectLog())
	if before == 0 {
		t.Fatal("the fixture recorded no history")
	}

	restored, err := RestoreEngine(cfg, e.Snapshot(), withNoopResolvers()...)
	if err != nil {
		t.Fatalf("RestoreEngine: %v", err)
	}
	if after := len(restored.EffectLog()); after != before {
		t.Errorf("history after a restore = %d entries, before = %d", after, before)
	}

	// And it has to be the same history, not merely the same length.
	want, _ := json.Marshal(e.Log())
	got, _ := json.Marshal(restored.Log())
	if string(got) != string(want) {
		t.Error("the restored history differs from the original")
	}
}

// TestRestore_SurvivesAnUnreadableBoard: the migration path.
//
// A board section this build cannot read is derived data, and derived data
// does not need a migration -- it needs to be thrown away and recomputed.
// Before the log was durable there was no second path, and thirteen bumps of
// SnapshotVersion meant thirteen sets of abandoned saves.
func TestRestore_SurvivesAnUnreadableBoard(t *testing.T) {
	e, cfg := logFixture(t)
	good := e.Snapshot()

	// A snapshot written by a future build: the board is in a shape this one
	// does not understand, but the log is a format it does.
	stored, err := json.Marshal(good)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var future map[string]interface{}
	if err := json.Unmarshal(stored, &future); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	future["version"] = SnapshotVersion + 1
	future["players"] = []interface{}{} // the board section is now nonsense
	future["round"] = 999
	scrambled, err := json.Marshal(future)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var snap Snapshot
	if err := json.Unmarshal(scrambled, &snap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	restored, err := RestoreEngine(cfg, &snap, withNoopResolvers()...)
	if err != nil {
		t.Fatalf("should have been rebuilt from the log, got %v", err)
	}

	// The scrambled board must have been ignored entirely, not merged.
	want, _ := json.Marshal(good)
	got, _ := json.Marshal(restored.Snapshot())
	if string(got) != string(want) {
		t.Errorf("rebuilding from the log did not reproduce the board:\n  want %s\n  got  %s", want, got)
	}
}

// TestHistory_CannotBeRewrittenThroughAPayload: "copies go in and copies come
// out" has to hold **inside** the payload too.
//
// The copy used to stop at the top level, so a composite value -- the actor
// list -- stayed shared with the caller, and one line of caller code could
// rewrite the engine's history. Replay and auditing both rest on that
// promise, and it held for scalar fields only.
func TestHistory_CannotBeRewrittenThroughAPayload(t *testing.T) {
	e, _ := logFixture(t)

	for _, ef := range e.EffectLog() {
		if ef.Args == nil {
			continue
		}
		for i := range ef.Args.Actors {
			ef.Args.Actors[i] = "rewritten"
		}
		for k := range ef.Args.Vars {
			ef.Args.Vars[k] = "rewritten"
		}
		for k := range ef.Data {
			ef.Data[k] = "rewritten"
		}
		ef.Cancel("rewritten")
	}

	for _, ef := range e.EffectLog() {
		if ef.Canceled && ef.Reason == "rewritten" {
			t.Fatal("the caller cancelled an entry in the engine's history")
		}
		if ef.Args == nil {
			continue
		}
		for _, id := range ef.Args.Actors {
			if id == "rewritten" {
				t.Fatal("the caller rewrote an actor list inside the engine's history")
			}
		}
		for _, v := range ef.Args.Vars {
			if v == "rewritten" {
				t.Fatal("the caller rewrote seat state inside the engine's history")
			}
		}
	}
}

// TestSubmitSkillUse_CannotBeRewrittenAfterValidation: the engine used to
// store the caller's pointer, so a submission could be validated as one thing
// and resolved as another.
func TestSubmitSkillUse_CannotBeRewrittenAfterValidation(t *testing.T) {
	e := newTestEngine(t, withNoopResolvers()...)
	mustAdd(t, e, "g1", roleGuard)
	mustAdd(t, e, "w1", roleWerewolf)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	use := &SkillUse{PlayerID: "g1", Skill: skillProtect, Targets: []string{"w1"}}
	if err := e.SubmitSkillUse(use); err != nil {
		t.Fatalf("SubmitSkillUse: %v", err)
	}
	use.Skill = skillKill
	use.Targets[0] = "g1"

	pending := e.Snapshot().PendingUses
	if len(pending) != 1 {
		t.Fatalf("pending submissions = %d, want 1", len(pending))
	}
	if pending[0].Skill != skillProtect || pending[0].Targets[0] != "w1" {
		t.Errorf("the caller rewrote a validated submission: %+v", pending[0])
	}
}

// TestSubmitSkillUse_RejectsNil: the kernel already declines to bring the
// game down for a nil effect arriving from a Resolver, and a host
// deserialising a submission off the wire can hand over a nil just as easily.
func TestSubmitSkillUse_RejectsNil(t *testing.T) {
	e := newTestEngine(t, withNoopResolvers()...)
	mustAdd(t, e, "g1", roleGuard)
	mustAdd(t, e, "w1", roleWerewolf)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.SubmitSkillUse(nil); err == nil {
		t.Error("a nil submission should be an error, not a panic")
	}
}

// TestSubmitSkillUse_IsBounded: how a Resolver treats repeated submissions is
// the rules' business; how much memory the kernel will hold on their behalf
// is the kernel's.
func TestSubmitSkillUse_IsBounded(t *testing.T) {
	e := newTestEngine(t, withNoopResolvers()...)
	mustAdd(t, e, "g1", roleGuard)
	mustAdd(t, e, "w1", roleWerewolf)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var err error
	for i := 0; i <= MaxPendingUses; i++ {
		err = e.SubmitSkillUse(&SkillUse{PlayerID: "g1", Skill: skillProtect, Targets: []string{"w1"}})
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Errorf("more than %d submissions were accepted from one player", MaxPendingUses)
	}
}
