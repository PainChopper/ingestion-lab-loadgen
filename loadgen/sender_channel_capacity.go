package main

func validSenderChannelCapacity(config config, value int) bool {
	return config.SenderChannel.Capacity.contains(value)
}

func (state *controlState) senderChannelCapacity() int {
	if state.controls.senderChannelCapacityConfigured {
		return state.controls.configuredSenderChannelCapacity
	}
	return state.controls.config.SenderChannel.Capacity.Initial
}
