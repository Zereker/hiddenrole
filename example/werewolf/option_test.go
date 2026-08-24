package werewolf

import (
	"github.com/Zereker/hiddenrole"
	"testing"
)

// TestWithLogger 日志只能在构造时给出，且真的接上了。
//
// 这个用例原本叫 TestWithLoggerAndMetrics，还一并测 WithMetrics。内核后来
// 把 Metrics 整套删了，理由写在它的 will-not-do 列表里：**指标口径是 host
// 的事**，内核硬编一套「该数哪些东西」就是替调用方做了决定。
//
// 删掉之后并不是数不了了——数法换成了下面这个：事件流本来就在，接一个
// OnEvent 处理器自己数即可，而且想数什么由 host 说了算。
func TestWithLogger(t *testing.T) {
	rec := &recordingLogger{}

	g := newRuleGameWith(t, nil, []EngineOption{hiddenrole.WithLogger(rec)},
		seats(wolf("w1"), villagers("v1", "v2", "v3"))...)

	g.end(PhaseNightWolf)
	g.mustUse("w1", SkillKill, "v1")
	g.end(PhaseNightWitch)

	if len(rec.infos) == 0 {
		t.Error("开局应当留下 Info 级日志")
	}
}

// TestMetricsAreTheHostsJob 指标由 host 经事件流自己数。
//
// 内核删掉 Metrics 之后，这是它留下的那条路。它比原来那套强的地方在于：
// 数什么、怎么分桶、要不要按角色拆，全由 host 决定，内核一个名字都不用认。
func TestMetricsAreTheHostsJob(t *testing.T) {
	counts := map[hiddenrole.EventType]int{}

	g := newRuleGame(t, nil, seats(wolf("w1"), villagers("v1", "v2", "v3"))...)
	g.e.OnEvent(func(ev *hiddenrole.Event) { counts[ev.Type]++ })

	// 一路推到夜晚结算：狼刀在那里才变成一件「发生过的事」。前面几个阶段
	// 只写内核原语，而原语按设计是不出门的——这个用例的后半段正是要这一点。
	g.end(PhaseNightWolf)
	g.mustUse("w1", SkillKill, "v1")
	for i := 0; i < 6 && !g.e.Status().Over && g.e.Status().Phase != PhaseDay; i++ {
		g.endAny()
	}

	if len(counts) == 0 {
		t.Fatal("事件流上什么都没数到")
	}
	// 内核自己的状态原语永远不出门，所以数不到它们才是对的。
	for _, primitive := range []hiddenrole.EventType{
		hiddenrole.EventSetAlive, hiddenrole.EventSetVar, hiddenrole.EventSetActors,
		hiddenrole.EventPhaseChanged, hiddenrole.EventDetour,
	} {
		if counts[primitive] > 0 {
			t.Errorf("内核原语 %v 被推到了事件流上，它不该出门", primitive)
		}
	}
}

// TestWithNilOption nil 选项与 nil 日志都不该让构造失败。
func TestWithNilOption(t *testing.T) {
	if _, err := hiddenrole.NewEngine(DefaultGameConfig(), nil, hiddenrole.WithLogger(nil)); err != nil {
		t.Errorf("nil 选项应当被忽略，实际 %v", err)
	}
}
