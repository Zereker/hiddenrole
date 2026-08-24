# Before you change this kernel

## The API is pinned, not frozen

The exported surface is pinned by [`testdata/api.golden`](testdata/api.golden),
guarded by `TestAPI_SurfaceIsPinned`: **change a name or a signature and the
test goes red**. The public sub-package `enginetest` is in there too, and
Appendix A of [`API.md`](API.md) is checked against the same file by
`TestAPI_AppendixMatchesTheGolden`.

Pinned means a change cannot happen **quietly**. It does not mean the surface
is settled -- [`API.md`](API.md) withdrew that claim and says why. This library
is pre-1.0: the API may change, and each change has to be deliberate and
recorded.

Changing the exported surface means doing three things together; any one of
them missing does not count:

1. Have a **specific reason you actually ran into**: some rules package could
   not be written because of it, the way around it would tell a lie, or the
   shape is wrong on its own terms when read against the mature systems this
   kernel resembles. "I think this is nicer" does not count.
2. Update the golden baseline:
   `go test . -run TestAPI_SurfaceIsPinned -update-api-golden`
3. Update `API.md`'s body. Appendix A is checked by a test, which will name
   exactly what is missing.

[`API.md` §15](API.md) lists the five kinds of pressure that are worth moving
the surface for. Only one of them has ever fired, and it was not on the list
until after it did -- so treat the list as a record, not a gate.

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

The engine and the rules packages live in two modules. During development use
`replace` to work against local sources:

```
// werewolf/go.mod
replace github.com/Zereker/hiddenrole => ../hiddenrole
```

A kernel change **must** be run against all three rules packages (they are the
only evidence of generality), along with each of their random games
(`RunFuzz`, 5000 games across the three).
