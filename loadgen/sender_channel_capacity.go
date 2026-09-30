package main

func validSenderChannelCapacity(policy policy, value int) bool {
	return policy.SenderChannel.Capacity.contains(value)
}

func (state *controlState) senderChannelCapacity() int {
	if state.controls.senderChannelCapacityConfigured {
		return state.controls.configuredSenderChannelCapacity
	}
	return state.controls.policy.SenderChannel.Capacity.Default
}
