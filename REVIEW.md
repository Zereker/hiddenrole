# An outside review

**Date:** 2026-08 · **Reviewed at:** `7a96b0b` · **Reviewer:** an outside reader,
first contact with the codebase.

## Scope and method

Every non-test source file was read end to end (5.8k lines, of which 2.6k are
comments), along with `README.md`, `DESIGN.md`, `ARCHITECTURE.md` and
`PRIOR-ART.md`. `make check` was run (build, vet, gofmt, lint, test, race --
all green; coverage 87.8%). Where a claim in this document could be settled by
running something rather than by reading, a throwaway test was written to
settle it; those tests were deleted afterwards and their output is quoted
inline.

**What this review deliberately does not do:** repeat `PRIOR-ART.md`. The
comparison against boardgame.io was done file by file, and OpenSpiel and
PettingZoo were consulted on elimination. Those conclusions stand and are not
revisited here.

**What it does instead:** point at that comparison's blind spot. All three
comparables belong to *one* class -- turn-based / multi-agent game frameworks.
This kernel's actual shape straddles three other mature fields, and each of
them casts a different structural problem into relief.

---

## 0. Verdict

The core holds up. On the information-boundary half it is genuinely ahead of
what it was compared against, and that is not a small thing (§5).

Three structural observations follow. Only the first is an inconsistency --
somewhere the design pays a cost and does not collect the benefit. The second
and third are trajectory notes, and the existing "wait until a game really
runs into it" discipline is the right response to both; they are recorded so
that the moment of collision is recognised when it comes.

A fourth section concerns the standard of evidence, and a fifth records three
invariants that are currently stated in comments and not enforced anywhere.

---

## 1. Event sourcing, built to ninety percent

The whole architecture -- one write point, resolution as a pure function,
state changes expressible only as `Effect`, replay -- **is event sourcing**.
Then `effectlog.go` says:

> For persistence use Snapshot: `Effect.Data` is a `map[string]interface{}`
> whose types degrade on a JSON round trip, and the effect log is designed for
> in-process replay and auditing, not as a storage format.

Against Akka Persistence, EventStoreDB or Kafka Streams, the master/slave
relationship here is **inverted**:

| | Mature event sourcing | Here |
|---|---|---|
| the source of truth | the event log | `Snapshot` |
| what a snapshot is for | replay acceleration; discardable and rebuildable at will | the only persistable truth |
| what the event log is for | persisted, crosses processes, feeds downstream pipelines | lives in one process's memory |
| schema evolution | upcasting: old events are read and lifted to the new shape | version mismatch is rejected outright; already at v13 |

The consequences are concrete, not aesthetic:

**The effect log does not survive a restore.** Measured:

```
original EffectLog entries = 4
restored EffectLog entries = 0
```

`RestoreEngine` rebuilds the board and never repopulates the history.
So "what actually happened on night three" -- named in `README.md` as a reason
the effect log earns its keep -- holds only for a game that lived from start
to finish inside a single process. The moment a real server needs an audit
trail is after a restart, a migration or a crash, which is exactly the moment
this one is empty.

**Everything downstream of persistence is unavailable.** Post-game analysis,
anti-cheat, replay for spectators, analytics: each of them wants a durable
event stream, and the durable artefact here is a state dump.

**The unserialisability is welded into the implementation, not just
documented.** `replayEffect` reads `effect.Data[phaseKey].(PhaseType)` and
`effect.Data[winnerKey].(Camp)`. Those assertions fail on anything that has
been through JSON, so the replay path cannot accept a log that was ever
written down.

The root cause is a single typing decision: `Data map[string]interface{}`. It
buys a third-party `Resolver` the freedom to attach an arbitrary payload, and
it costs the entire event stream its durability. The mature engines resolve
this the other way round -- **the payload is typed, closed and serialisable,
and the extensibility lives in the event type being open**, not in the payload
being untyped. That is how `EventType` already works here (`kernelEvents` is a
table, not a numeric range, precisely so third-party types are first-class);
`Data` is the one place the same principle was not applied.

This is the one place the design pays event sourcing's full price -- the
purity constraint on resolvers, the single write point, the determinism
discipline, the invariant harness -- and collects about half its value.

## 2. The unit of extension is not the unit of the domain

`DESIGN.md` §8.2 lists "one `Resolver` per phase, composition by
hand-wrapping" as speculative, with no game having run into it. That ranking
is defensible on its own terms. It is recorded higher here for a different
reason: **the gap between that structure and what the README promises.**

`ARCHITECTURE.md` principle 1 is "Phase-centric, not role-centric", and the
benefit claimed is that adding a role does not mean editing the engine. That
is true. But the real cost centre is not the engine, it is **that phase's
resolver**. In werewolf, the guard's, the witch's and the wolves' effects are
combined in one night-resolution resolver; a new role that acts at night means
editing it. So:

- the kernel does not recognise roles ✅
- **roles are not composable units** ❌

The mature prior art for this exact problem is not in `PRIOR-ART.md`: the
Magic: The Gathering rules engines (XMage, Forge). There the unit of
authorship is the *card*, twenty-five thousand of them written by different
people, and they interact because the **engine owns an interaction
substrate**:

| What an MTG engine provides | The counterpart here |
|---|---|
| replacement effects -- intercept an effect somebody else produced | `Effect.Cancel` + `SetsAlive()`, but **no interception pipeline** |
| the stack and priority -- ordering, and windows to respond in | none; ordering is decided inside a single resolver |
| layers, timestamps, dependency -- ordering simultaneous continuous effects | one sentence ("the order must be decided by the board alone"), no mechanism |
| state-based actions -- re-checked continuously after every action | the victory check, run once per phase transition |

`Effect.SetsAlive()` is documented as the hook "an extension that wants to
intercept a death needs". There is nowhere for that extension to run except
inside the resolver that produced the death. The idiot surviving an exile
works because the vote resolver both kills and checks for the idiot. A role
written by somebody else cannot intercept it without wrapping the whole
original resolver -- which is precisely the "composition by hand-wrapping"
already recorded.

**The recommendation is not to build that substrate.** Fifteen roles is not
twenty-five thousand cards, global interaction is the norm in this genre
rather than the exception, and building MTG's layer system now would be
textbook over-engineering -- exactly what the will-not-do list exists to
prevent. The recommendation is narrower: **align the wording with the
structure.** "Adding a role does not require editing the engine" reads, to a
first-time reader, as "roles are plugins". Saying "a new role is a new phase
plus a new resolver, and roles acting in the same phase share one resolver"
costs one sentence and removes the mismatch.

## 3. The phase machine is re-deriving statechart semantics

`PhaseConfig` currently carries `Steps`, `NextPhase`, `Timeout`, `EndsRound`,
`ClearsRoundVars`; at runtime there is also `GOTO_PHASE` and the detour queue.
Translated into the vocabulary of Harel statecharts / W3C SCXML:

| Mechanism here | Its name there |
|---|---|
| `NextPhase` | default transition |
| `GOTO_PHASE` | guarded transition |
| the detour queue | **deferred / internal event queue** |
| `ClearsRoundVars` | onentry action |
| `EndsRound` | onexit action |
| the four `NIGHT_*` phases | a **compound state**, here flattened |

The semantics arrived at are correct -- detour outranks goto outranks the
default exit is exactly SCXML's ordering -- and they were arrived at by being
burned, which is the honest way. The note is about trajectory: each new
requirement has so far added one more boolean to `PhaseConfig`, and the
interaction *between* those booleans has no home other than prose. Today that
prose is the comment above `settled := !endNow && !e.state.hasPendingDetour()`
in `engine.go`, which has to explain four conditions at once and does so at
some length.

Two things follow, neither of which is "adopt statecharts" (for two consumers
that would be over-engineering):

- **Treat a third boolean as a signal.** When the next declarative flag is
  proposed, the question to ask is not "is this flag right" but "is the
  combination still describable" -- that is the moment the phase machine wants
  a formal semantics rather than another field.
- **"The night as a whole" is currently inexpressible.** Any property shared
  by the four `NIGHT_*` phases has to be declared four times. That is the
  concrete thing flattening costs, and it is the shape a compound state would
  buy back.

## 4. The standard of evidence is not applied to itself

`DESIGN.md` contains a good sentence:

> a generalisation from a sample of two is not a generalisation, it is a
> guess. Wait for the third.

The README's central argument is that three unrelated rules packages run on
this kernel. All three live in `Zereker/werewolf`, by one author. Measured by
the standard quoted above, that is **one mind's blind spots exercised three
times**, not independent validation. boardgame.io's generality was
demonstrated by strangers.

This does not devalue the three packages -- the scars they produced are real,
and `RoleType`-as-two-layers in One Night is the kind of finding only a real
implementation yields. It calibrates the strength of the conclusion: "wrote
the third one with zero breaking API changes" is weaker evidence than the
README's framing implies, because the third author was the same person who
owned the API.

**The API is frozen, and no third party has ever exercised it.** That is the
one test that none of the existing machinery -- the golden file, the seven
invariants, the mutation testing -- can substitute for.

A direct consequence worth fixing on its own: **the repository has no git
tags.** `git tag -l` is empty. A frozen API, a golden test guarding
signatures, 928 lines of `API.md` -- and a consumer running `go get` receives
a pseudo-version. Semantic versioning is the form in which "frozen" is
promised to the outside world, and that step is currently missing.

## 5. Three invariants that live only in comments

This project's own recurring diagnosis is that *a rule guarded by no test is
only a sentence*. Three documented invariants are currently in that state.
They are listed here rather than in a defect tracker because each one is a
stated invariant rather than a slip, and because each was verified by running
it.

**I. "History cannot be rewritten" does not hold for composite payloads.**
`Effect.clone()` promises that copies go into the log and copies come out.
The copy is shallow over `Data`'s values, so a slice payload -- the one
`NewSetActorsEffect` stores -- is shared with the caller:

```
before mutation:                  [g1 w1]
engine log after caller mutation: [HACKED w1]
```

Replay and auditing both rest on that promise, and it currently holds for
scalar fields only.

**II. Skill validation can be bypassed after the fact.** `SubmitSkillUse`
validates, then stores the caller's `*SkillUse` pointer. The caller still
holds it:

```go
use := &SkillUse{PlayerID: "g1", Skill: skillProtect, Targets: []string{"w1"}}
e.SubmitSkillUse(use)   // validated as PROTECT
use.Skill = skillKill   // engine now holds {Skill:KILL Targets:[g1]}
```

The resolver is handed a submission that never passed validation. Both
`Snapshot()` and `restorePendingUses` copy the target slice defensively; the
submission entry point is the one that does not.

**III. The kernel defends against third-party nils in one direction only.**
`applyEffects` and `applyEffect` each guard against a nil effect from a
resolver, with a comment explaining that it is not worth bringing the game
down for. `SubmitSkillUse(nil)` dereferences immediately and panics. The
standard is right; it was applied to the rules-package side and not to the
host side.

A fourth, lesser one: `pendingUses` has no bound and no de-duplication -- one
player submitting the same skill a thousand times is accepted a thousand
times, and all thousand enter the snapshot. How a resolver treats repeated
submissions is correctly the rules' business; how much memory the kernel will
allocate on their behalf is the kernel's.

---

## 6. Where this is stronger than the comparables

Recorded so the review is not read as one-sided; these are conclusions an
outside reader reached independently.

**The write constraint is held up by a signature, not by discipline.** A
boardgame.io move may mutate `G` freely (immer only turns the mutation into an
immutable update); XMage's cards mutate the game object directly.
`Resolve(uses, GameView) []*Effect` cannot reach mutable state at the type
level. Since snapshots, replay and auditing all rest on the single write
point, holding it with the compiler rather than with a convention is a real
structural advantage over both comparison classes.

**The information boundary is a first-class concept with a floor that cannot
be configured away.** For contrast, boardgame.io's default secret-state
implementation, `PlayerView.STRIP_SECRETS`, deletes keys **named** `secret` --
a naming convention doing safety-critical work. Splitting "what may this
player see" from "who should be told about this", and making the kernel's own
primitives structurally unable to leave the building, is the most valuable
thing in this library. `PublicPlayerInfo` being unable to *hold* a variable
bag turns a runtime judgement into a compile-time one, and that is the right
instinct.

**"The kernel may offer a default, not make law" is executed cleanly.**
Demoting `Alive` from arbiter to default is the clearest instance; most
frameworks keep hard-coding at that exact point.

**The verification apparatus is above its weight class.** Byte-for-byte
snapshot comparison across a replay, plus mutation testing to check that the
invariants themselves have teeth (`checkSameBehaviour` exists because a
"snapshot loses Actors" mutation survived), is a standard most projects of
this size never reach.

---

## 7. If only one thing gets done

**Make the effect log the persistable truth (§1).** The reasons, in order:

1. It is the only place where the full price has been paid and half the value
   collected. Everything the benefit needs is already built.
2. It subsumes the snapshot-versioning problem. Once history is durable, a
   snapshot becomes a discardable accelerator, and format evolution stops
   being a cliff at every bump -- which matters at v13 with an outright
   rejection on mismatch.
3. It need not break the frozen API. Tightening `Effect.Data`'s type would be
   breaking; adding a serialisable parallel path is not.

Second priority is not a code change: **get a fourth rules package written by
somebody else** (§4). None of the existing verification machinery substitutes
for it, and it is the missing precondition for the freeze the API already
declares.

Sections 2 and 3 should keep waiting for a real collision, in line with this
project's own discipline. Section 5 is small work whose value is that three
sentences currently doing invariant duty would start being enforced.
