package main

func validReaderChannelCapacity(policy policy, value int) bool {
	return policy.ReaderChannel.Capacity.contains(value)
}

func (state *controlState) readerChannelCapacity() int {
	if state.readerChannelCapacityConfigured {
		return state.configuredReaderChannelCapacity
	}
	return state.policy.ReaderChannel.Capacity.Default
}
