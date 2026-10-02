package main

func validReadBatchSize(config config, value int) bool {
	return config.Reader.ReadBatchSize.contains(value)
}

func (state *controlState) readBatchSize() int {
	if state.controls.configuredReadBatchSize != 0 {
		return state.controls.configuredReadBatchSize
	}
	return state.controls.config.Reader.ReadBatchSize.Initial
}
