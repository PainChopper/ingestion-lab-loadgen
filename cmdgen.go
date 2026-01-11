package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type cmdType int

const (
	setMode cmdType = iota
	setPulse
	getStatus
	quit
)

type command struct {
	kind  cmdType
	mode  mode
	pulse time.Duration
	reply chan statusSnapshot
}

type statusSnapshot struct {
	Mode  string
	Pulse string
	Limit string
}

func startHttpServer(out chan command) {
	mux := http.NewServeMux()

	statusHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		replyChan := make(chan statusSnapshot, 1)
		cmd := command{kind: getStatus, reply: replyChan}
		out <- cmd
		snapshot := <-replyChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snapshot)
	}

	mux.HandleFunc("/control", controlHandler)
	mux.HandleFunc("/status", statusHandler)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	server.ListenAndServe()
}

func controlHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
}

// func startStdInReader(out chan command) {
// 	scanner := bufio.NewScanner(os.Stdin)
// 	for scanner.Scan() {
// 		line := strings.TrimSpace(scanner.Text())
// 		if len(line) == 0 {
// 			continue
// 		}
// 		words := strings.Fields(line)

// 		switch words[0] {
// 		case "mode":
// 		case "pulse":
// 		case "status":
// 		case "quit":
// 		default:
// 			fmt.Printf("Unknown command: %s\n", words[0])
// 		}
// 	if err := scanner.Err(); err != nil {

// 	}
// }

// func startCommandDriver(out chan command) {
// 	time.Sleep(3 * time.Second)
// 	out <- command{kind: setMode, mode: burst}
// 	time.Sleep(3 * time.Second)
// 	out <- command{kind: setPulse, pulse: 500 * time.Millisecond}
// 	time.Sleep(3 * time.Second)
// 	out <- command{kind: getStatus}
// 	time.Sleep(3 * time.Second)
// 	out <- command{kind: quit}
// }
