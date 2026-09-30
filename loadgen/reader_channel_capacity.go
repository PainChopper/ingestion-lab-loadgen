package main

func validReaderChannelCapacity(policy policy, value int) bool {
	return policy.ReaderChannel.Capacity.contains(value)
}

func (state *controlState) readerChannelCapacity() int {
	if state.controls.readerChannelCapacityConfigured {
		return state.controls.configuredReaderChannelCapacity
	}
	return state.controls.policy.ReaderChannel.Capacity.Default
}
