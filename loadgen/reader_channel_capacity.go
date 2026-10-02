package main

func validReaderChannelCapacity(config config, value int) bool {
	return config.ReaderChannel.Capacity.contains(value)
}

func (state *controlState) readerChannelCapacity() int {
	if state.controls.readerChannelCapacityConfigured {
		return state.controls.configuredReaderChannelCapacity
	}
	return state.controls.config.ReaderChannel.Capacity.Initial
}
