package main

func validReadBatchSize(policy policy, value int) bool {
	return policy.Reader.ReadBatchSize.contains(value)
}

func (state *controlState) readBatchSize() int {
	if state.controls.configuredReadBatchSize != 0 {
		return state.controls.configuredReadBatchSize
	}
	return state.controls.policy.Reader.ReadBatchSize.Default
}
