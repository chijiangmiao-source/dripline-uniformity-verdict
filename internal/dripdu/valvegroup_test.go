package dripdu

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkEmitters(t *testing.T, flows []string, ids ...string) []ValveEmitter {
	t.Helper()
	if len(ids) == 0 {
		ids = make([]string, len(flows))
		for i := range flows {
			ids[i] = "e" + string(rune('a'+i))
		}
	}
	emitters := make([]ValveEmitter, len(flows))
	for i, f := range flows {
		flow, err := ParseDesignFlow(f)
		require.NoError(t, err, f)
		emitters[i] = ValveEmitter{ID: ids[i], DesignFlow: flow}
	}
	return emitters
}

func mustCapacity(t *testing.T, s string) *big.Rat {
	t.Helper()
	capacity, err := ParsePumpCapacity(s)
	require.NoError(t, err, s)
	return capacity
}

func TestPlanSingleGroupWhenEverythingFits(t *testing.T) {
	// 20+20+20+20 = 80 <= 100: one group carries every emitter and keeps
	// 20 lph of spare capacity.
	groups := PlanValveGroups(mustCapacity(t, "100"),
		mkEmitters(t, []string{"20", "20", "20", "20"}))

	require.Len(t, groups, 1)
	g := groups[0]
	assert.Equal(t, 1, g.Number)
	assert.Equal(t, []string{"ea", "eb", "ec", "ed"}, g.MemberIDs)
	assert.Equal(t, 0, g.TotalDesignFlow.Cmp(big.NewRat(80, 1)))
	assert.Equal(t, 0, g.RemainingCapacity.Cmp(big.NewRat(20, 1)))
}

func TestPlanExactCapacityStaysInCurrentGroup(t *testing.T) {
	// 40+30+30 = 100 exactly saturates the pump: the third emitter still
	// belongs to the first group. The 25 then opens a new group, the 50
	// joins it (75 <= 100), and the final 50 would reach 125, so it starts
	// a third group on its own.
	groups := PlanValveGroups(mustCapacity(t, "100"),
		mkEmitters(t, []string{"40", "30", "30", "25", "50", "50"}))

	require.Len(t, groups, 3)

	assert.Equal(t, 1, groups[0].Number)
	assert.Equal(t, []string{"ea", "eb", "ec"}, groups[0].MemberIDs)
	assert.Equal(t, 0, groups[0].TotalDesignFlow.Cmp(big.NewRat(100, 1)))
	assert.Equal(t, 0, groups[0].RemainingCapacity.Sign(), "saturated group has zero remaining capacity")

	assert.Equal(t, 2, groups[1].Number)
	assert.Equal(t, []string{"ed", "ee"}, groups[1].MemberIDs)
	assert.Equal(t, 0, groups[1].TotalDesignFlow.Cmp(big.NewRat(75, 1)))
	assert.Equal(t, 0, groups[1].RemainingCapacity.Cmp(big.NewRat(25, 1)))

	assert.Equal(t, 3, groups[2].Number)
	assert.Equal(t, []string{"ef"}, groups[2].MemberIDs)
	assert.Equal(t, 0, groups[2].TotalDesignFlow.Cmp(big.NewRat(50, 1)))
	assert.Equal(t, 0, groups[2].RemainingCapacity.Cmp(big.NewRat(50, 1)))
}

func TestPlanExactThousandthsSums(t *testing.T) {
	// 3.333+3.333+3.334 = 10.000 exactly: three-decimal inputs sum without
	// drift. The trailing 0.5 would reach 10.5 > 10.2, so it opens a second
	// group and the first keeps the exact 0.2 of remaining capacity.
	groups := PlanValveGroups(mustCapacity(t, "10.2"),
		mkEmitters(t, []string{"3.333", "3.333", "3.334", "0.5"}))

	require.Len(t, groups, 2)
	assert.Equal(t, []string{"ea", "eb", "ec"}, groups[0].MemberIDs)
	assert.Equal(t, 0, groups[0].TotalDesignFlow.Cmp(big.NewRat(10, 1)))
	assert.Equal(t, 0, groups[0].RemainingCapacity.Cmp(big.NewRat(1, 5)))
	assert.Equal(t, []string{"ed"}, groups[1].MemberIDs)
	assert.Equal(t, 0, groups[1].TotalDesignFlow.Cmp(big.NewRat(1, 2)))
	assert.Equal(t, 0, groups[1].RemainingCapacity.Cmp(big.NewRat(97, 10)))
}

func TestPlanExactFitBoundaryFollowUp(t *testing.T) {
	// A follow-up emitter that brings the group to exactly capacity joins
	// the group rather than opening a new one.
	groups := PlanValveGroups(mustCapacity(t, "10.5"),
		mkEmitters(t, []string{"3.333", "3.333", "3.334", "0.5"}))

	require.Len(t, groups, 1)
	assert.Equal(t, []string{"ea", "eb", "ec", "ed"}, groups[0].MemberIDs)
	assert.Equal(t, 0, groups[0].TotalDesignFlow.Cmp(big.NewRat(21, 2)))
	assert.Equal(t, 0, groups[0].RemainingCapacity.Sign())
}

func TestPlanEveryEmitterStartsOwnGroup(t *testing.T) {
	// 60+60 > 100: each emitter closes the previous group.
	groups := PlanValveGroups(mustCapacity(t, "100"),
		mkEmitters(t, []string{"60", "60", "60", "60"}))

	require.Len(t, groups, 4)
	for i, g := range groups {
		assert.Equal(t, i+1, g.Number)
		assert.Len(t, g.MemberIDs, 1)
		assert.Equal(t, 0, g.TotalDesignFlow.Cmp(big.NewRat(60, 1)))
		assert.Equal(t, 0, g.RemainingCapacity.Cmp(big.NewRat(40, 1)))
	}
}

func TestPlanGroupCountIsMinimal(t *testing.T) {
	// Greedy in-order packing is optimal for consecutive groups: any plan
	// respecting the installation order needs at least as many groups.
	// Pairs summing to exactly 100 share a group; the 10 after the
	// saturating 100 must open its own group.
	flows := []string{"30", "70", "40", "60", "50", "50", "100", "10"}
	groups := PlanValveGroups(mustCapacity(t, "100"), mkEmitters(t, flows))

	require.Len(t, groups, 5)
	assert.Equal(t, []string{"ea", "eb"}, groups[0].MemberIDs)
	assert.Equal(t, []string{"ec", "ed"}, groups[1].MemberIDs)
	assert.Equal(t, []string{"ee", "ef"}, groups[2].MemberIDs)
	assert.Equal(t, []string{"eg"}, groups[3].MemberIDs)
	assert.Equal(t, []string{"eh"}, groups[4].MemberIDs)
	assert.Equal(t, 0, groups[3].RemainingCapacity.Sign())
}

func TestPlanIsDeterministicAcrossRuns(t *testing.T) {
	flows := []string{"12.5", "7.25", "3.125", "22.75", "9.5", "14.125"}
	first := PlanValveGroups(mustCapacity(t, "40"), mkEmitters(t, flows))
	second := PlanValveGroups(mustCapacity(t, "40"), mkEmitters(t, flows))
	assert.Equal(t, first, second)
}

func TestParsePumpCapacityAndDesignFlow(t *testing.T) {
	// Same plain-decimal form as flow_lph but no 100 lph ceiling.
	for input, want := range map[string]string{
		"0.001":   "1/1000",
		"100":     "100/1",
		"100.001": "100001/1000",
		"250.5":   "501/2",
		"10000":   "10000/1",
	} {
		capacity, err := ParsePumpCapacity(input)
		require.NoError(t, err, input)
		wantRat, ok := new(big.Rat).SetString(want)
		require.True(t, ok)
		assert.Equal(t, 0, capacity.Cmp(wantRat), "capacity input %s", input)

		flow, err := ParseDesignFlow(input)
		require.NoError(t, err, input)
		assert.Equal(t, 0, flow.Cmp(wantRat), "design flow input %s", input)
	}

	for _, input := range []string{"0", "0.0", "0.000"} {
		_, err := ParsePumpCapacity(input)
		assert.ErrorIs(t, err, ErrCapacityRange, "input %q", input)
		_, err = ParseDesignFlow(input)
		assert.ErrorIs(t, err, ErrDesignFlowRange, "input %q", input)
	}
	for _, input := range []string{"1.2345", "0.0001", "01", "1e2", "-1", ".5", "1.", "+1"} {
		_, err := ParsePumpCapacity(input)
		assert.ErrorIs(t, err, ErrCapacityFormat, "input %q", input)
		_, err = ParseDesignFlow(input)
		assert.ErrorIs(t, err, ErrDesignFlowFormat, "input %q", input)
	}
}
