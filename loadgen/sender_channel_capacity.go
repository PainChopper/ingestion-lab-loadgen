package main

func validSenderChannelCapacity(config config, value int) bool {
	return config.SenderChannel.Capacity.contains(value)
}

func (state *controlState) senderChannelCapacity() int {
	return state.controls.senderChannelCapacity
}
