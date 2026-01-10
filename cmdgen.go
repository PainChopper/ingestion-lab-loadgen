package main

import "time"

func cmdgen(out chan command) {
	time.Sleep(3 * time.Second)
	burstMode := Burst
	out <- command{mode: &burstMode}
	time.Sleep(3 * time.Second)
	pulse := time.Duration(500 * time.Millisecond)
	out <- command{pulse: &pulse}
}
