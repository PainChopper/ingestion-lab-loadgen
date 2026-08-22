package main

func (state *controlState) handleRunCommand(pipeline func()) {
	if state.lifecycle.run() {
		pipeline()
	}
}
