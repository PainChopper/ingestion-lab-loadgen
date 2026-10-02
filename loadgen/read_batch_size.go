package main

func validReadBatchSize(config config, value int) bool {
	return config.Reader.ReadBatchSize.contains(value)
}

func (state *controlState) readBatchSize() int {
	return state.controls.readBatchSize
}
