package avalon_test

import (
	"math/rand"
	"testing"

	"github.com/Zereker/hiddenrole"
	"github.com/Zereker/hiddenrole/enginetest"
	"github.com/Zereker/hiddenrole/example/avalon"
)

// fuzz_test.go 把七条内核不变量跑在一套**真实规则**上。
//
// enginetest 里那套是为了验证内核而临时编出来的，它的形状是我照着内核该有的
// 样子倒推的——好用，但它证明不了「一套没有为内核而设计的规则也成立」。
// 阿瓦隆不是为这些不变量写的：它先在别处写完，才被搬进来。
//
// 它撞到的东西恰好是内核最近改动最大的几处：
//
//	游戏级变量     第几轮任务、连续否决数、轮到谁带队，都不属于任何玩家
//	运行时点名     队伍在提名阶段选出，在任务阶段使用
//	GOTO_PHASE     表决通过去任务，否决回提名——静态图表达不了
//	进出动作分离   队伍标记活到下次提名，回合数跟着第几轮任务，两者不重合
//	一个人都不出局 elimination 在这套规则里根本没用到
func TestAvalon_HoldsTheKernelInvariants(t *testing.T) {
	enginetest.RunFuzz(t, enginetest.FuzzSpec{
		Games:    120,
		MaxSteps: 80,
		Setup:    setup,
		Act:      act,
		WantEnd:  true,
		MustSee:  []string{"five", "seven"},
	})
}

// 两种人数的板子，角色配置固定，随机的是打法。
var boards = map[int][]enginetest.Seat{
	5: {
		{ID: "a", Role: avalon.RoleMerlin},
		{ID: "b", Role: avalon.RolePercival},
		{ID: "c", Role: avalon.RoleLoyalServant},
		{ID: "d", Role: avalon.RoleAssassin},
		{ID: "e", Role: avalon.RoleMorgana},
	},
	7: {
		{ID: "a", Role: avalon.RoleMerlin},
		{ID: "b", Role: avalon.RolePercival},
		{ID: "c", Role: avalon.RoleLoyalServant},
		{ID: "d", Role: avalon.RoleLoyalServant},
		{ID: "e", Role: avalon.RoleAssassin},
		{ID: "f", Role: avalon.RoleMorgana},
		{ID: "g", Role: avalon.RoleOberon},
	},
}

func setup(rng *rand.Rand) enginetest.Game {
	size, label := 5, "five"
	if rng.Intn(2) == 0 {
		size, label = 7, "seven"
	}
	return enginetest.Game{
		Config:  avalon.DefaultConfig(),
		Options: avalon.Options(),
		Seats:   boards[size],
		Labels:  []string{label},
	}
}

// act 按当前阶段出招。
//
// 通用的随机提交在这套规则上几乎每次都会被拒：提名要正好 N 个人，任务票只有
// 队伍成员能投，刺杀只有刺客能提交。所以取招这件事必须由规则包自己做——这正
// 是 enginetest 把 Act 交给调用方而不是自己实现的原因。
func act(e *hiddenrole.Engine, rng *rand.Rand) {
	alive := e.AlivePlayerIDs()
	if len(alive) == 0 {
		return
	}

	switch e.Status().Phase {
	case avalon.PhasePropose:
		propose(e, rng, alive)
	case avalon.PhaseTeamVote:
		vote(e, rng, alive)
	case avalon.PhaseMission:
		mission(e, rng, alive)
	case avalon.PhaseAssassin:
		assassinate(e, rng, alive)
	}
}

// propose 让队长提满一支队伍。谁是队长由引擎点名，问它就是了。
func propose(e *hiddenrole.Engine, rng *rand.Rand, alive []string) {
	leader := ""
	for _, id := range alive {
		if contains(e.AllowedSkills(id), avalon.SkillPropose) {
			leader = id
			break
		}
	}
	if leader == "" {
		return
	}

	size := avalon.MissionSize(len(alive), e.Status().Round)
	picked := map[string]bool{}
	for len(picked) < size && len(picked) < len(alive) {
		picked[alive[rng.Intn(len(alive))]] = true
	}
	for id := range picked {
		//nolint:errcheck // 被拒是这里的合法结果
		_ = e.SubmitSkillUse(&hiddenrole.SkillUse{
			PlayerID: leader, Skill: avalon.SkillPropose, Targets: []string{id},
		})
	}
}

// vote 全员表决。偶尔故意全票否决，把连续否决那条路走出来。
func vote(e *hiddenrole.Engine, rng *rand.Rand, alive []string) {
	forceReject := rng.Intn(5) == 0
	for _, id := range alive {
		skill := avalon.SkillApprove
		if forceReject || rng.Intn(3) == 0 {
			skill = avalon.SkillReject
		}
		//nolint:errcheck // 同上
		_ = e.SubmitSkillUse(&hiddenrole.SkillUse{PlayerID: id, Skill: skill})
	}
}

// mission 队伍成员各投一票。谁在队伍里同样问引擎。
func mission(e *hiddenrole.Engine, rng *rand.Rand, alive []string) {
	for _, id := range alive {
		allowed := e.AllowedSkills(id)
		switch {
		case contains(allowed, avalon.SkillMissionFail) && rng.Intn(2) == 0:
			//nolint:errcheck // 同上
			_ = e.SubmitSkillUse(&hiddenrole.SkillUse{PlayerID: id, Skill: avalon.SkillMissionFail})
		case contains(allowed, avalon.SkillMissionSuccess):
			//nolint:errcheck // 同上
			_ = e.SubmitSkillUse(&hiddenrole.SkillUse{PlayerID: id, Skill: avalon.SkillMissionSuccess})
		}
	}
}

// assassinate 刺客指认一个人。指对了坏人赢，指错了好人赢。
func assassinate(e *hiddenrole.Engine, rng *rand.Rand, alive []string) {
	for _, id := range alive {
		if !contains(e.AllowedSkills(id), avalon.SkillAssassinate) {
			continue
		}
		//nolint:errcheck // 同上
		_ = e.SubmitSkillUse(&hiddenrole.SkillUse{
			PlayerID: id,
			Skill:    avalon.SkillAssassinate,
			Targets:  []string{alive[rng.Intn(len(alive))]},
		})
		return
	}
}

func contains(skills []hiddenrole.SkillType, want hiddenrole.SkillType) bool {
	for _, s := range skills {
		if s == want {
			return true
		}
	}
	return false
}
