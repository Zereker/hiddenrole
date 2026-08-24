# What this is, and who it is for

> This document exists because the repository could answer "what is this
> kernel shaped like" ([DESIGN.md](DESIGN.md)), "what does it promise"
> ([API.md](API.md)) and "how does it compare" ([PRIOR-ART.md](PRIOR-ART.md))
> long before it could answer **"who imports it, to build what, instead of
> what"**. That last question is this one.
>
> The positioning decision itself was already made in the code: the module was
> renamed from `werewolf` to `hiddenrole`, the kernel was cut away from the
> rules, and three rulesets were put down as peers under `example/`. What was
> missing was writing down what that makes the thing.

---

## 1. One sentence

**`hiddenrole` is the adjudication core of a hidden-role game: who may act,
what the board becomes when they do, and who is entitled to know it.**

Everything with a clock, a socket or a disk in it is deliberately yours.

That is not a modesty clause, it is the product boundary. The three things
above are the parts that are hard to get right and impossible to test by
playing; timers, rooms and persistence are the parts every backend engineer
already knows how to write and each wants written their own way.

---

## 2. What you get, and what you still write

| The kernel gives you | You still write |
|---|---|
| a phase machine driven by declarative configuration, with runtime overrides (`GOTO_PHASE`, detours) | **when** a phase ends -- there is no clock; `PhaseConfig.Timeout` is advice |
| one write point for state, so every change is describable, loggable and replayable | where any of it is **stored** |
| per-player views (`PlayerView`) and event routing (`AudienceOf`) with a floor the rules cannot lower | the **transport**: sockets, rooms, matchmaking, reconnection |
| "who has yet to act" (`PhaseReadiness`), separating must-act from may-act | the **prompts**: wording, UI, nudges, what a timeout does |
| snapshots and effect-log replay, with random-game invariants you can point at your own ruleset (`enginetest`) | the **rules**: roles, skills, ways to die, victory, who tells whom what |

The last line is the one people misread. The kernel ships **no game**. Three
rulesets live under [`example/`](example) and each is installed entirely
through public options -- they are evidence and reference, not a runtime
dependency you inherit.

---

## 3. Who it is for

Ordered by how well the design actually fits, not by how many people there
are.

### 3.1 A Go backend for a hidden-role game

A werewolf server, a Discord or QQ bot, a mini-game backend, a table for a
club. You bring the transport and the timers; the kernel keeps the board and
draws the information boundary.

**What it buys you specifically:** the boundary is the part hand-written
servers get wrong, and they get it wrong late -- the leak is invisible until
someone reads their own packets. Here, `PlayerView(id)` is sendable as-is and
`PublicPlayerInfo` **structurally cannot carry** a state bag, so the mistake
is a compile error rather than a bug report from a player who saw the wolf
roster.

[`example/werewolf/netserver`](example/werewolf/netserver) is the whole wiring
in about 400 lines: receive an event, ask `AudienceOf` who should get it,
write to those connections.

### 3.2 A simulation or agent harness

Bots, search, or LLM agents playing social deduction against each other.

This audience fits the design almost too well, and the repository never says
so: **no networking, no clock, resolution as a pure function of the board, a
full effect log, per-player views, byte-comparable snapshots.** That is the
list a research harness normally hand-rolls badly. `enginetest.RunFuzz` runs
thousands of random games against seven kernel-level invariants; the same
machinery runs an agent tournament.

The one thing this audience will hit is randomness during play: the kernel has
none on purpose, and its history is recorded in [PRIOR-ART.md](PRIOR-ART.md)
(added, no users, removed). Deal before the game is created, as all three
rulesets do, or roll in the host and feed the result in as a skill submission.

### 3.3 Someone reading how a general kernel gets derived

Not a user, but an honest audience, and given the ratio of prose to code
(roughly 370 KB of documentation to 5,400 lines of kernel) arguably the one
the repository is currently best at serving. The interesting documents are the
ones that record being wrong: the [scars](example/onenight/SCARS.md) a ruleset
left, the "we judged this a gap and the evidence came back backwards" entry,
the feature added from a comparison table and deleted for having no users.

---

## 4. Who it is not for

| If you want | Use |
|---|---|
| a playable werewolf game today | a finished product -- OpenWerewolf, one of the self-hosted mafia servers. This is a library; it has no UI and no lobby |
| networking, rooms, matchmaking, accounts | a game-server framework. This has none of it, and adding it is on the [will-not-do list](DESIGN.md) |
| a real-time game, or one with perfect information | something else entirely -- see the four assumptions below |
| any of this in TypeScript or Python | [boardgame.io](https://github.com/boardgameio/boardgame.io) or [Open Mafia Engine](https://github.com/open-mafia/open_mafia_engine); there is nothing to import here |

### The four assumptions

The kernel assumes all four. A game missing any one of them is out of range,
and no amount of configuration will bring it back in:

1. the players are a **group**, not one player against a system
2. time is divided into **named steps** that somebody declares over
3. actions are **resolved in batches** at the end of a step, not immediately
4. **some people know things others do not**

Werewolf, The Resistance / Avalon, One Night, Blood on the Clocktower, Secret
Hitler, Spyfall all satisfy them. Chess satisfies 1 and 2 and fails 3 and 4.

---

## 5. What it is measured against

|  | boardgame.io | Open Mafia Engine | a playable mafia server | **hiddenrole** |
|---|---|---|---|---|
| language | TypeScript | Python | various | **Go** |
| scope | any turn-based game | mafia-family games | one game | the hidden-role family |
| **does the engine know the game?** | no | **yes** -- factions, abilities and roles are engine concepts | yes, entirely | **no** -- the kernel owns two role values, and neither is a role |
| the information boundary | one `playerView(G, ctx, id)` function you write | knowledge modelled per role inside the engine | ad hoc, per feature | structured `PlayerView` + `AudienceOf` + a floor the rules cannot lower |
| ships transport and a client | yes | no | yes | no |
| state changes | a reducer; a move may mutate freely | events and actions | anywhere | one write point, enforced by the `Resolver` signature |

Read the third row down the columns and the niche is visible: **domain-shaped
enough that concealment is a first-class thing, general enough that the engine
still does not know which game it is running.** Open Mafia Engine has the first
without the second (its engine knows what a faction is); boardgame.io has the
second without the first (concealment is a function you are handed a blank for).

In Go, a search of the ecosystem turns up neither -- no general turn-based
state kernel and no hidden-role engine. **That is the actual opening**, and it
is a narrow one: it is a bet that somebody wants to build this class of game
in Go, not a bet that this design beats the others.

---

## 6. Where the project actually is

Positioning that only lists strengths is marketing. The parts a reader should
know before adopting:

| | |
|---|---|
| **maturity** | the kernel carries three unrelated rulesets, the third of which forced zero exported-name changes. That is real evidence of generality, and it is the only evidence there is |
| **users** | every user this repository can name is **inside this repository**. There is no external adopter to point at, no production deployment, no issue tracker full of other people's edge cases |
| **stability** | the API is **not frozen**. It was declared frozen once, and breaking changes shipped inside `v1` afterwards, which is not something Go's import compatibility rule allows. The policy that replaces the declaration is in [STABILITY.md](STABILITY.md) |
| **the docs-to-code ratio** | around 370 KB of prose against 5,400 lines of kernel. Some of that is the product (§3.3); some of it is a reader having to get through an essay before learning that this does not do networking. The README is being pulled back towards the second reading |
| **known gaps** | victory resolves to a single `Camp` (One Night's tanner has run into it); a skill's target must be a player ID (looking at a centre card has run into it); aliveness is a privileged bit rather than a canonical key. Each is recorded with its trigger condition in [DESIGN.md §8](DESIGN.md) |

### On the three rulesets under `example/`

They implement the play of games that other people published -- The
Resistance and its Avalon variant, One Night Ultimate Werewolf, and the
Chinese table ruleset of Werewolf. Packages are named after the **structure**
of play (`missions`, `onenight`), not after anybody's trademark; rules are
implemented from public sources cited in each package, with no text copied.

**This project is not affiliated with, or endorsed by, the publishers of those
games.**

---

## 7. What would change this positioning

Written down so it can be honoured rather than re-argued:

| If this happens | The positioning moves |
|---|---|
| a fourth ruleset cannot be written without breaking the kernel | "general kernel for the family" weakens to "a kernel for these three"; §5's third row stops being the differentiator |
| somebody ships a product on it | §3.1 stops being a hypothesis. Until then the primary audience is a bet, and the roadmap should be read as one |
| an agent or research harness adopts it | §3.2 moves to the top, and randomness during play stops being deferrable |
| the boundary turns out to be re-derivable in an afternoon | the core value claim was wrong, and what is left is a phase machine with a good effect log -- worth less, and worth saying so |
