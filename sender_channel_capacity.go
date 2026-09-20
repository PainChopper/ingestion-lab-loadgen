package main

func validSenderChannelCapacity(policy policy, value int) bool {
	return policy.SenderChannel.Capacity.contains(value)
}

func (state *controlState) senderChannelCapacity() int {
	if state.senderChannelCapacityConfigured {
		return state.configuredSenderChannelCapacity
	}
	return state.policy.SenderChannel.Capacity.Default
}
