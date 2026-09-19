package main

func validQueue1Capacity(policy policy, value int) bool {
	return policy.Queue1.Capacity.contains(value)
}

func (state *controlState) queue1Capacity() int {
	if state.queue1CapacityConfigured {
		return state.configuredQueue1Capacity
	}
	return state.policy.Queue1.Capacity.Default
}
