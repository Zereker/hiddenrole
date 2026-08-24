# Before you change this kernel

## The API is frozen

The exported surface is pinned jointly by [`API.md`](API.md) and
[`testdata/api.golden`](testdata/api.golden), guarded by
`TestAPI_SurfaceIsPinned`: **change a name or a signature and the test goes
red**. The public sub-package `enginetest` is in there too.

Changing the exported surface means doing three things together; any one of
them missing does not count:

1. Have a **specific reason you actually ran into** -- some rules package
   could not be written because of it, or the way around it would tell a lie.
   "I think this is nicer" does not count.
2. Update the golden baseline:
   `go test . -run TestAPI_SurfaceIsPinned -update-api-golden`
3. Update `API.md` (the body and Appendix A)

[`API.md` §15](API.md) lists the four conditions under which reopening the
freeze is worth it.

## A rule cannot live only in a comment

**Tests passing is not the same as tests being useful.** Every behaviour
change needs **mutation verification**: undo the change, run the tests, and
confirm they really do go red. If they do not, that rule was only a comment.

Real problems this has caught in this project:

- Removing the consumption of the actor list turned **not one test red** --
  both rules packages name actors again every time, so a stale list was always
  overwritten.
- The first version of the random games compared snapshot bytes only, and when
  the snapshot serialiser itself drops a field it drops it on both sides --
  the "snapshot loses `Actors`" mutation survived on the spot.

Write what you mutated and what happened into the commit message.

## The kernel knows no game

The test is one sentence: **can the kernel judge this correctly without
knowing what game it is?**

If it cannot, it belongs to the rules. See [`DESIGN.md` §1](DESIGN.md).

The kernel owns only a handful of values, and every one of them is defended
individually in [`DESIGN.md` §7](DESIGN.md) with **usage data from three rules
packages**. **That table may only get shorter.**

## A change has to be verified against all three rules packages

The kernel and the three rules packages live in one module, in four separate
packages: the kernel at the root, the games under [`games/`](games). A kernel
change **must** be run against all three (they are the only evidence of
generality), along with each of their random games (`RunFuzz`, 5000 games
across the three) -- which is what `go test ./...` does.

Being one module changes nothing about the boundary: Go does not let one
package touch another's unexported identifiers, so "the rules use only the
public API" is still enforced by the compiler. The kernel has no `internal/`
at all -- `enginetest` is public on purpose, see [`API.md`](API.md) -- so every
entry point `games/` uses is one a third party can use too.

## What language to write in

Comments are English in the kernel and per-package in `games/`, and the rule
is **the language the game's own rules are written in**, not consistency
across the repository.

| Package | Language | Implements |
|---|---|---|
| the kernel (root) | **English** | nothing; it knows no game |
| [`games/werewolf/`](games/werewolf) | **Chinese** | the Chinese ruleset of Werewolf |
| [`games/missions/`](games/missions) | **English** | The Resistance and its Avalon variant |
| [`games/onenight/`](games/onenight) | **English** | One Night Ultimate Werewolf |
| [`example/`](example) | **Chinese** | all three examples host a game of Werewolf, so they follow that package |

The kernel is a library strangers import, and its comments carry the reasoning
-- why it recognises no value of its own, why the three faces are not merged.
Locking half of that behind one language throws half of it away.

`games/werewolf/` implements what is played at a Chinese table: 屠边/屠城,
同守同救, the guard who may not guard twice running, 上帝 as host, the
12-player standard board. Those concepts are natively Chinese; "屠边" rendered
as *side-wipe* has already lost the 神职/平民 structure it rests on.

The other two go the other way. Merlin, Percival, Mordred, Morgana, Oberon,
the Robber, the Troublemaker, the Tanner -- **those names are originally
English**, and the Chinese ones are translations, of which there is more than
one. Writing `Merlin` is writing the word on the rulebook; writing 梅林 asks
the reader to translate back before they can match the English rulebook or
almost any community discussion.

Before changing a package's comment language, say what language its rules were
written in.

### Two READMEs for the werewolf package

| | What it is | Who keeps it current |
|---|---|---|
| [`games/werewolf/README.md`](games/werewolf/README.md) | the full reference, 800-odd lines, in Chinese | follows the code |
| [`games/werewolf/README.en.md`](games/werewolf/README.en.md) | **a short, independent English overview**, 200 lines | only when "what is this, how do I start" changes |

**The second is not a translation of the first, and should not be filled out
into one.** It is the English reader's front door: what this is, whether it is
usable, where the kernel is. Precisely because it does not chase the Chinese
version, it does not drift.

## Running it

```sh
go test ./...          # unit + integration + 5000 random games
go test -race ./...    # the TCP server example runs under -race
make lint              # golangci-lint
make check             # all of the above, plus gofmt and go vet
```

The three examples all run as-is; after changing the API, check they are still
alive:

```sh
go run ./example              # each interface demonstrated
printf 'run\nquit\n' | go run ./example/cli   # play a game start to finish
go run ./example/extension    # a third-party role (the idiot)
```

## What a good change looks like

### The test has to be able to catch it

**Passing tests is not the same as useful tests.** Every behaviour change in
this repository has been **mutation-verified**: back the change out, run the
tests, confirm they really do go red. What was mutated and how many went red
goes in the commit message and the CHANGELOG, so others can check rather than
take it on trust.

A real example: the guard's no-consecutive-guard restriction was left out of
the snapshot for an entire release. The random games of the day compared only
phase and round, so save-and-restore walked both sides through a whole game in
step -- with different rules decisions. It took **comparing the exported
snapshots byte for byte** to catch it.

So when you open a PR, say in passing: if your change were removed, which test
goes red? Not being able to answer usually means the test is measuring
something else.

### State changes only through an Effect

A `Resolver` gets a read-only `GameView` and can express a state change only
by returning an `Effect`. That is not a style convention; it is the premise
that snapshots, replay and auditing all rest on -- hide state in a resolver's
fields and a restored game is wrong **and does not report it**.

Where state goes is a matter of scope. Writes always go through
`NewSetVarEffect(scope, key, value)` and reads through
`GameView.Var(scope, key)`, with the scope picking one of four cells:

| | unowned | owned by a player |
|---|---|---|
| **the whole game** | `ScopeGame` (score, whose turn) | `ScopeGame.Of(id)` (the witch's potions) |
| **this round** | `ScopeRound` (tonight's kill) | `ScopeRound.Of(id)` (who was guarded tonight) |

### Adding a role should not mean changing the engine

This is the standard the whole design is held to. If your change needs an
`if role == X` or a `case EventY` inside the engine, stop and ask whether the
abstraction is missing an opening -- **it usually is**. The eight extension
points are in the README; the built-in roles go through the same ones and have
no privileges.

The kernel has two state primitives: set alive, set a variable (four scopes,
above). Plus two control directives: rewrite the next phase, queue a detour.
`KILL` / `ELIMINATE` / `SHOOT` are names the rules give to "what happened";
the state machine does not know them -- emit a `KILL` effect on its own and
nobody dies.

### Rules need a source

Each rules package is measured against a baseline (a Wikipedia article or the
publisher's rulebook), named in its own README. When changing rules behaviour,
write down in a test or a comment which clause you are following and why you
read it that way. Rules that tables disagree about should become configuration
(see each package's `GameConfig`), not a choice made on the user's behalf.

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org/):
`feat:` / `fix:` / `refactor:` / `docs:` / `test:` / `chore:`, with `!` for
breaking changes.

The body says **why**, not what -- what is in the diff. If a change fixes a
specific bug, explain that bug: what triggers it, what it looks like, why it
went unnoticed. None of that is readable from the diff.

## Compatibility

**Since v1.5.0 the exported API is a promise.** A breaking change needs a
major version, and by Go's module rules that means changing the module path
(`/v2`), which changes every user's imports. That cost is kept on purpose: it
makes "should we break this" a question that has to be answered seriously.

So: first work out whether you can avoid breaking anything. An extra option, a
new function, an interface wrapper with a default implementation -- one of
those usually does the job.

A changed snapshot structure means bumping `SnapshotVersion`, even when the
exported API did not change.

## Releasing

Releases run from GitHub Actions: **Actions → Release → Run workflow**, fill in
the version. The tag and the Release are both created by the workflow, and the
release notes come from the matching section of the CHANGELOG -- no section,
no release. No local permissions needed.
