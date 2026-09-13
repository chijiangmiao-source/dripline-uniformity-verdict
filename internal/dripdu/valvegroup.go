package dripdu

import "math/big"

// ValveEmitter is one validated emitter on a branch, in installation order.
type ValveEmitter struct {
	ID string
	// DesignFlow is the emitter's design flow in litres per hour, strictly
	// greater than 0. Request validation guarantees it never exceeds the
	// pump capacity, so every emitter can start a group of its own.
	DesignFlow *big.Rat
}

// ValveGroup is one maximal run of consecutive emitters whose combined
// design flow fits within the pump's available flow.
type ValveGroup struct {
	// Number is the 1-based group index in installation order.
	Number int
	// MemberIDs are the emitter ids of the group, in installation order.
	MemberIDs []string
	// TotalDesignFlow is the exact sum of the members' design flows; it
	// never exceeds the pump capacity.
	TotalDesignFlow *big.Rat
	// RemainingCapacity is capacity − TotalDesignFlow, exact and never
	// negative; it is exactly 0 when the group saturates the pump.
	RemainingCapacity *big.Rat
}

// PlanValveGroups packs emitters into as few consecutive valve groups as the
// pump capacity allows. Emitters are accumulated in installation order; the
// current group is closed as soon as adding the next emitter would push the
// combined design flow past capacity, and a total exactly equal to capacity
// still belongs to the current group. Because every emitter individually
// fits within capacity, each group has at least one member and the single
// greedy pass needs no backtracking to reach the minimum group count.
func PlanValveGroups(capacity *big.Rat, emitters []ValveEmitter) []ValveGroup {
	groups := make([]ValveGroup, 0, 1)
	members := make([]string, 0, len(emitters))
	total := new(big.Rat)
	closeGroup := func() {
		groups = append(groups, ValveGroup{
			Number:            len(groups) + 1,
			MemberIDs:         members,
			TotalDesignFlow:   new(big.Rat).Set(total),
			RemainingCapacity: new(big.Rat).Sub(capacity, total),
		})
	}

	for _, e := range emitters {
		if len(members) > 0 {
			candidate := new(big.Rat).Add(total, e.DesignFlow)
			if candidate.Cmp(capacity) > 0 {
				closeGroup()
				members = make([]string, 0, len(emitters))
				total = new(big.Rat)
			}
		}
		members = append(members, e.ID)
		total.Add(total, e.DesignFlow)
	}
	if len(members) > 0 {
		closeGroup()
	}
	return groups
}
