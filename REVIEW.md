# An outside review

**Date:** 2026-08 · **Reviewed at:** `7a96b0b` · **Reviewer:** an outside reader,
first contact with the codebase.

> **Status.** Sections 1, 2 and 3 were acted on, and section 5's three
> invariants along with them; see [the status section](#8-what-was-done) at
> the end. The findings below are left as they were written, in the present
> tense, so that what was claimed can still be checked against what was done.

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

---

## 8. What was done

All three structural sections were acted on in one change, breaking the API
freeze deliberately. What follows is what each turned into, so that the
finding and its fix can be read against each other.

**§1, the log.** `Effect`'s payload was split the way `EventType` already
split: the type stays open, the payload became closed. `Effect.Args` carries
the kernel's primitives as typed fields; `Effect.Data` carries the rules'
payload as strings. `GameLog` is now the durable record, with its own version
that moves on a different beat from the board's. A `Snapshot` carries the log
that produced it, so a restore keeps the history -- it used to drop it, four
entries in and zero out. And a board section this build cannot read is
rebuilt by replaying the log rather than rejected, which ends the cliff that
had abandoned thirteen generations of saved games.

The durability is checked rather than asserted: `enginetest`'s replay
invariant now marshals the log to JSON, reads it back, and replays *that*.
Replaying the in-memory objects would have passed on a log no storage could
hold, which is exactly what used to happen.

**§2, the substrate.** `Interceptor` gives a rule somewhere to stand between
another rule's effect and the write point. It is a pipeline, not Magic's
replacement-effect system: each interceptor sees each effect once in
registration order, and its output is what the next one sees -- terminating by
construction, deterministic, no priority or layers. A replaced effect stays in
the log, cancelled, so the history records what was about to happen as well as
what did.

This closes the case the review said hurt most, and **not** the whole gap:
resolution is still one function per phase, so two roles *acting* in the same
phase still share it. What changed is that a role *reacting* to another role's
action no longer needs to. The review's other recommendation was taken as
well -- `ARCHITECTURE.md` principle 1 now says "a new role is a new
`PhaseConfig` plus a new `Resolver`" instead of "adding a role does not
require editing the engine", because the second is true and invites the reader
to expect plug-in roles.

**§3, the phase machine.** `EndsRound` and `ClearsRoundVars` were entry and
exit actions wearing another name; they are now `PhaseAction` lists, so the
next lifetime is a constant rather than a fourth boolean. `PhaseConfig.Parent`
groups phases into compound ones, and transitions use the statechart rule:
strip the groups both ends share, and what is left is really being left and
entered. "The night begins from a clean board" is declared once on `NIGHT`,
and `GameView.InPhase(NIGHT)` replaces enumerating its members.

The review did **not** recommend adopting statecharts, and this is not that.
There is no hierarchy of transitions, no parallel regions, no history states.
What was taken is the one rule that makes grouping mean anything.

**§5, the three invariants.** All three now hold and are tested: `clone` deep
-copies the payload, `SubmitSkillUse` stores a copy and rejects nil, and
pending submissions are bounded. The fourth, lesser item is done too.

**§4 and the rest.** The evidence-standard finding stands unaddressed by
definition -- it asks for a fourth rules package by somebody else, which is
not something a change to this repository can supply. The missing git tag is
likewise still missing, and now matters more: the surface moved.

One thing was fixed that the review did not name. `enginetest.RunFuzz` had no
caller in this repository, so the seven invariants -- the strongest
verification here -- never ran against the kernel itself. There is now a
ruleset in `enginetest/fuzz_test.go` built to exercise what the kernel owns
(a compound phase, an interceptor, a detour queue, runtime actors, a victory
condition) and deliberately not resembling werewolf. It is what gives any
confidence that a rewrite this size did not break the invariants quietly.

`API.md`'s Appendix A was regenerated, and is now **checked** by
`TestAPI_AppendixMatchesTheGolden` rather than kept in step by remembering to
-- the same wound this project keeps diagnosing elsewhere, sitting in the
middle of the document that declares the API frozen. And `API.md` §15, which
lists what would reopen the freeze, gained a fifth row: all four it had
anticipated pressure from a *rules package*, and this break came from reading
the kernel against the systems it resembles. A rules package tells you what
hurts; it cannot tell you what is misshapen.
