package main

const (
	defaultQueue1Capacity = 2
	maxQueue1Capacity     = 8_192
)

func validQueue1Capacity(value int) bool {
	if value == 0 {
		return true
	}
	return value > 0 && value <= maxQueue1Capacity && value&(value-1) == 0
}

func (state *controlState) queue1Capacity() int {
	if !state.queue1CapacityConfigured {
		return defaultQueue1Capacity
	}
	return state.configuredQueue1Capacity
}
