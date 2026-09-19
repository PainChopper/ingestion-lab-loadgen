package main

const defaultReadBatchSize = 50_000

func validReadBatchSize(value int) bool {
	return value >= 1_000 && value <= 100_000 && value%1_000 == 0
}

func (state *controlState) readBatchSize() int {
	if state.configuredReadBatchSize == 0 {
		return defaultReadBatchSize
	}
	return state.configuredReadBatchSize
}
