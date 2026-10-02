package main

func validReaderChannelCapacity(config config, value int) bool {
	return config.ReaderChannel.Capacity.contains(value)
}

func (state *controlState) readerChannelCapacity() int {
	return state.controls.readerChannelCapacity
}
