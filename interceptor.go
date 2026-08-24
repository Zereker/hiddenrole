// interceptor.go is the effect-interception substrate: how a rule reacts to
// something **somebody else** is about to do.
//
// Every other extension point answers a question the kernel asks it ("how
// does this phase resolve", "who won", "who may hear this"). This one is the
// other direction: it lets a rule stand in the path of an effect it did not
// produce, and say "not like that".
//
// # Why it had to exist
//
// Effect.SetsAlive was documented as the hook "an extension that wants to
// intercept a death needs" -- the idiot surviving an exile by flipping their
// card, written once and working against every way of dying, because a wolf
// kill, a poisoning and a gunshot all end at the same primitive. But there
// was nowhere for that code to run. Effects went from the phase's Resolver
// straight to the write point, so the only way to stand in between was to be
// that Resolver. Which meant:
//
//   - the unit of authorship was the **phase**, not the role: two roles
//     acting in one phase shared one resolver, and adding a role meant
//     editing it;
//   - a role could not react to another role's action unless the same
//     function produced both;
//   - "wrap the original resolver" was the only composition available, and
//     it requires the wrapper to know what it is wrapping.
//
// The comparison that made this concrete is the card-game engines (XMage,
// Forge): twenty-five thousand cards written by different people interact
// because the **engine** owns the substrate they meet in -- replacement
// effects, ordering, a place to stand. Owning none of it is what kept roles
// here from being composable units.
//
// # What it deliberately is not
//
// This is not Magic's replacement-effect system. There is no priority, no
// stack, no timestamp-and-dependency ordering. It is a **pipeline**: each
// interceptor sees each effect once, in registration order, and what it
// returns is what the next one sees. That terminates by construction, needs
// no fixpoint, and is deterministic without anybody having to reason about
// layers -- which is the right size for a genre with fifteen roles rather
// than twenty-five thousand cards.

package hiddenrole

// Interceptor stands between an effect and the write point.
//
// It is called for **every** effect on its way in, whoever produced it: a
// phase resolver, the opening GameSetup, another interceptor's replacement,
// or Engine.Apply.
//
// Returning nil is "no opinion", and is what an interceptor returns almost
// every time -- look at the effect, decide it is not yours, hand it back
// untouched. Returning a non-empty slice **replaces** the effect with those
// effects. Returning an empty non-nil slice replaces it with nothing, which
// is the rare case: prefer calling Cancel on the effect and returning nil, so
// that the attempt and its reason stay in the log.
//
//	// The idiot survives being exiled: the card flips instead.
//	hiddenrole.InterceptorFunc(func(ef *hiddenrole.Effect, view hiddenrole.GameView) []*hiddenrole.Effect {
//		alive, ok := ef.SetsAlive()
//		if !ok || alive {
//			return nil // not a death; not mine
//		}
//		p, found := view.Player(ef.TargetID)
//		if !found || p.Role != roleIdiot || view.Var(hiddenrole.ScopeGame.Of(p.ID), keyFlipped) != "" {
//			return nil
//		}
//		return []*hiddenrole.Effect{
//			hiddenrole.NewEffect(eventFlip, "", p.ID),
//			hiddenrole.NewSetVarEffect(hiddenrole.ScopeGame.Of(p.ID), keyFlipped, hiddenrole.VarPresent),
//		}
//	})
//
// Same contract as every other extension point: read the GameView, touch no
// state, and **do not call back into the Engine** -- it is called while the
// engine holds its lock, and the consequence is a hang, not an error.
//
// The view it is handed reflects the effects applied **before** this one in
// the same batch, and not this one. So an interceptor sees the board an
// effect is about to land on, which is what "may this be allowed" needs.
type Interceptor interface {
	Intercept(effect *Effect, view GameView) []*Effect
}

// InterceptorFunc lets a plain function satisfy Interceptor.
type InterceptorFunc func(effect *Effect, view GameView) []*Effect

// Intercept implements Interceptor.
func (f InterceptorFunc) Intercept(effect *Effect, view GameView) []*Effect {
	return f(effect, view)
}

// WithInterceptor installs an interceptor.
//
// Unlike the other options this one **appends**: several rules may each want
// to stand in the path, and "registering twice keeps the last" would silently
// disable one of them. They run in registration order, and that order is part
// of the rules' configuration -- a game whose interceptors disagree has to
// decide which one is outermost, and the kernel will not decide it for them.
//
// Order is also why registration is construction-only, like everything else:
// a pipeline that could be rearranged mid-game would make the effect log
// depend on when somebody registered rather than on what happened.
func WithInterceptor(interceptor Interceptor) EngineOption {
	return func(e *Engine) error {
		if interceptor == nil {
			return WrapError(CodeInvalidConfig, "interceptor must not be nil")
		}
		e.interceptors = append(e.interceptors, interceptor)
		return nil
	}
}

// interceptReason is the veto reason written on an effect that an
// interceptor replaced.
//
// The original stays in the log, cancelled, rather than being dropped. An
// audit trail that showed only the replacement would answer "what happened"
// but not "what was about to happen and who stopped it" -- and the second
// question is exactly the one a contested game gets asked.
const interceptReason = "replaced by an interceptor"

// runInterceptors passes one effect through the pipeline and returns what
// should actually be applied. The caller must hold e.mu.
//
// Each interceptor sees the output of the one before it, so a chain works;
// and each sees each effect at most once, so it terminates. A replacement
// produced by interceptor i is offered to i+1 and onwards, never back to i --
// which is what stops two rules from replacing each other's replacements
// forever.
func (e *Engine) runInterceptors(effect *Effect) []*Effect {
	if len(e.interceptors) == 0 {
		return []*Effect{effect}
	}

	current := []*Effect{effect}
	for _, interceptor := range e.interceptors {
		next := make([]*Effect, 0, len(current))
		for _, ef := range current {
			if ef == nil {
				continue
			}
			// A cancelled effect is already settled. Offering it round would
			// invite an interceptor to resurrect what another one vetoed, and
			// "who wins a veto" is a question this pipeline deliberately does
			// not open.
			if ef.Canceled {
				next = append(next, ef)
				continue
			}

			replacement := interceptor.Intercept(ef, e.view())
			if replacement == nil {
				next = append(next, ef)
				continue
			}

			ef.Cancel(interceptReason)
			next = append(next, ef)
			for _, r := range replacement {
				if r != nil {
					next = append(next, r)
				}
			}
		}
		current = next
	}
	return current
}
