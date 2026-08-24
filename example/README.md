# Examples: real rulesets running on the kernel

These are the kernel's **users**, kept in the same repository as the kernel
itself. That placement is the point, and it is a change: they used to live in
a separate repository, which meant the kernel's own CI could not see a single
one of its consumers. A breaking change could go green here and be discovered
downstream, or not be discovered at all.

They serve three purposes at once, in ascending order of value:

1. **A starting point.** Somebody writing their own ruleset has two complete
   ones to read, each assembled entirely through public options, with no back
   door into the kernel. The compiler guarantees that last part: these
   packages sit outside `package hiddenrole`.
2. **A demonstration that the kernel knows nothing.** No two of these rulesets
   share a single value -- not a phase, not a role, not a skill -- and the
   kernel needs no change to run either.
3. **Verification.** This is the one that earns them their place. Their tests
   were written against the kernel's behaviour, not its implementation, so
   they catch what the kernel's own tests cannot: whether a change breaks the
   thing the library exists for. `go test ./...` runs them.

Each also runs the seven invariants from
[`enginetest`](../enginetest/) over random games of itself. That matters more
than it sounds: the ruleset inside `enginetest`'s own test is one written *for*
the invariants, and a ruleset written for a test cannot show that the test
holds for rulesets that were not.

## What is here

| Package | Game | What it exercises that the other does not |
|---|---|---|
| [`avalon`](avalon/) | The Resistance / Avalon | **Nobody is ever eliminated.** Game-scoped state (which mission, consecutive rejections, whose turn to lead), a team chosen in one phase and used in the next, and a branch (`GOTO_PHASE`) that a static graph cannot express |

Werewolf is the other one and has not been brought across yet.

## The rules of engagement

A package here may use **only** what an outside author could use. If one of
them ever needs something the kernel does not export, that is a finding about
the kernel, not a reason to reach inside.

Their scars -- the places where a ruleset ran into the kernel's shape -- are
recorded next to the code that hit them (`avalon/SCARS.md`), because a
limitation written down where it was met is worth more than one filed in a
list somewhere.
