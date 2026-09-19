package main

func validReadBatchSize(policy policy, value int) bool {
	return policy.Reader.ReadBatchSize.contains(value)
}

func (state *controlState) readBatchSize() int {
	if state.configuredReadBatchSize != 0 {
		return state.configuredReadBatchSize
	}
	return state.policy.Reader.ReadBatchSize.Default
}
