# Stability policy

> This replaces the freeze declaration that used to head [API.md](API.md).
> The discipline it described was good and is kept below; the word "frozen"
> was not accurate and is dropped.

---

## 1. Where this module stands

The kernel is published as `github.com/Zereker/hiddenrole` at **v1**, and
**breaking changes have already shipped inside v1** -- v1.6.0 removed
thirteen exported names from the kernel (see [CHANGELOG.md](CHANGELOG.md)).

Go's rule is that the import path *is* the compatibility promise: two versions
sharing an import path must be compatible, and a `go get -u` must never break
a build. Shipping v1.5 -> v1.6 with removals broke that rule. Nobody outside
this repository is known to have been hit, which is luck, not a defence.

This is not a rule the project failed to know about.
[CONTRIBUTING.md](CONTRIBUTING.md) already said "a breaking change needs a
major version, and by Go's module rules that means changing the module path"
while that release was being prepared. A rule written only in a guide is
enforced by nobody -- the same failure mode this project names elsewhere as
"a rule cannot live only in a comment".

**The API is not frozen.** It is a v1 surface carrying known debt, with the
next breaking batch queued for a major version.

---

## 2. The policy

### 2.1 v1 takes no more breaking changes

While the import path is `github.com/Zereker/hiddenrole`, releases may:

- **add** exported names,
- **fix** behaviour that contradicts documented intent,
- change anything unexported.

They may not remove or rename an exported name, change a signature, or change
documented behaviour that a caller could reasonably depend on.

### 2.2 Breaking changes queue for v2

They go into the `## v2 (queued)` section of [CHANGELOG.md](CHANGELOG.md) and
ship **together**, as:

```
module github.com/Zereker/hiddenrole/v2
```

with a `v2.0.0` tag. A major version is a fresh import path, so v1 keeps
working for anyone already on it and nothing is broken under anybody's feet.
Batching them is deliberate: each major version costs every caller an edit, so
one edit should buy every improvement that was waiting.

### 2.3 The argument this replaces

The v1.6.0 decision was written down, and it was not careless:

> Go requires a `/vN` suffix on modules at major version 2 and above, which
> would change every user's import path. Paying the breakage once and staying
> on the v1 line is the better trade for a library with no known importers
> yet.

Three things are wrong with it, and they are worth stating because the same
argument will be made again:

1. **"No known importers" is not a state you can observe.** The premise
   expires silently -- the first adopter does not announce themselves, and by
   the time they report a broken build the release is already out.
2. **The two costs are not comparable.** `/v2` costs an import-path edit,
   paid deliberately by someone who chose to upgrade. Breaking v1 costs a
   build that fails on a `go get -u` nobody chose. One is an inconvenience,
   the other is an outage in somebody else's CI.
3. **This repository's own standard forbids it.** "A rule cannot live only in
   a comment" is the project's quality slogan. "The public API is a
   commitment" is exactly such a rule if the toolchain can violate it while
   every test stays green.

The honest alternative, if the `/vN` suffix is genuinely unacceptable, is not
to break v1 quietly -- it is to say in the README that this library does not
follow semantic import versioning and that callers must pin an exact version.
That is a worse offer, and it is at least a true one.

### 2.4 Deprecate before removing

A name on its way out gets a `// Deprecated: use X instead` comment in a v1
release **before** the v2 that removes it. `staticcheck` and every editor
surface it; a removal with no warning ahead of it does not.

### 2.5 The three disciplines that stay

These came from the freeze declaration and were always the useful part of it:

1. **A breaking change needs a specific reason somebody ran into** -- a
   ruleset that could not be written, or a workaround that would tell a lie.
   "I think this is nicer" does not count. The tracking condition for each
   queued change is written down with it (§3).
2. **Adding is harder than removing.** What is added cannot be taken back
   before the next major; before removing, answer "who uses this".
3. **A change cannot happen quietly.** Changing the exported surface means
   updating [`testdata/api.golden`](testdata/api.golden) and
   [API.md](API.md) in the same commit, and `TestAPI_SurfaceIsPinned` fails
   until you do.

`TestAPI_SurfaceIsPinned` is a **change detector, not a freeze**. It does not
judge whether a change is good; it makes sure no change happens silently.

---

## 3. The v2 queue

Nothing here moves on its own. Each entry carries the condition that would
promote it from "known" to "doing", and the entries with no evidence yet stay
put no matter how tidy they would be.

| Change | Why | Trigger |
|---|---|---|
| `VictoryChecker` returns `[]Camp` rather than one `Camp` | One Night's tanner wins alongside the villagers, which is a routine outcome of the base game | **one ruleset has run into it.** Ships with the next major regardless, since a second collision would only confirm it |
| `SubmitSkillUse` stops aliasing the caller's `*SkillUse` (copy on submit, or take a value) | the engine keeps the caller's pointer and back-fills `Phase`/`Round` into it; mutating the struct after submitting changes what the resolver sees, and that is a data race under any concurrent host | **already true today**; a correctness fix that cannot be made without touching the signature or the documented ownership |
| `PhaseStep` gains a target kind, so a target need not be a player ID | One Night looks at a centre card | the encoding (an index folded into the skill name) starts lying, or the combinations explode. Costs 15 ugly lines today |
| aliveness demoted from a privileged bool to a canonical key | Blood on the Clocktower's poisoned / drunk are parallel state bits; the missions ruleset uses aliveness nowhere yet pays for it in a snapshot field, a view field and three defaults | a ruleset cannot be written because "alive" is one bit |
| `RoleSystem` / `SkillAnnounce` move into the werewolf package | of three rulesets only werewolf has a host | a fourth and fifth ruleset still cannot use them |

Additive candidates -- an `OnEvent` unsubscribe, deep-copying `Effect.Data`,
JSON tags on the host-facing structs -- are **not** in this table. They do not
need a major version, so they ship in v1 when someone needs them.

---

## 4. The snapshot format is versioned separately

`SnapshotVersion` (currently 13) tracks the on-disk shape of `Snapshot`, and
has nothing to do with the module version. `RestoreEngine` rejects any version
it does not recognise, and **there is no migration path**: a bump invalidates
every saved game.

That is acceptable only while saves are short-lived. If you persist snapshots
across releases, pin the kernel version alongside the data, or convert your
saves before upgrading.

---

## 5. What is not promised, at any version

- **Performance.** No workload has reported it slow, and optimisation follows
  measurement.
- **The keys inside `Effect.Data`.** They are the kernel's internal
  convention; read effects with `SetsAlive()` / `SetsVar()` rather than
  reaching into the map.
- **Log output and error message wording.** Branch on `ErrorCode` or the
  `Err*` sentinels, never on a message string.
- **The `example/` packages' APIs.** They are reference rulesets. They follow
  the kernel; they do not carry compatibility promises of their own.
