package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	// --- NEW IMPORT ---
	// Import the official Go trace parsing library.
	"golang.org/x/trace"
)

// --- CORS Middleware ---
func withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		h(w, r)
	}
}

// rootHandler serves the main.html file.
func rootHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "main.html")
}

func main() {
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/trace", withCORS(traceHandler))
	println("Go Visualizer server starting on http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

func traceHandler(w http.ResponseWriter, r *http.Request) {
	code, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read code", 400)
		return
	}

	dir, _ := ioutil.TempDir("", "gtrace")
	defer os.RemoveAll(dir)
	tmpFile := filepath.Join(dir, "main.go")
	if err := ioutil.WriteFile(tmpFile, code, 0644); err != nil {
		http.Error(w, "Failed to write temp file", 500)
		return
	}

	// Use a context with a timeout to prevent long-running programs from hanging the server.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", tmpFile)
	cmd.Dir = dir
	output, _ := cmd.CombinedOutput()

	// Check if the command was killed due to the timeout.
	if ctx.Err() == context.DeadlineExceeded {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "Execution timed out after 5 seconds.",
			"go_run_output": "This often happens with long-running servers or programs with infinite loops. Please ensure your program terminates to generate a complete trace.",
		})
		return
	}

	tracePath := filepath.Join(dir, "trace.out")
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "trace.out not generated",
			"go_run_output": string(output),
			"run_error":     "This can happen if there was a compile error in the code.",
		})
		return
	}

	// --- MODIFICATION: Call the new analyzeTrace function ---
	jsonData, err := analyzeTrace(tracePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "Failed to analyze trace.out",
			"analyze_error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonData)
}

// --- NEW analyzeTrace function ---
// This function now uses the golang.org/x/trace library directly.
func analyzeTrace(tracePath string) (map[string]interface{}, error) {
	// 1. Open the trace.out file for reading.
	f, err := os.Open(tracePath)
	if err != nil {
		return nil, fmt.Errorf("could not open trace file: %w", err)
	}
	defer f.Close()

	// 2. Parse the trace file using the official library.
	result, err := trace.Parse(f, "")
	if err != nil {
		return nil, fmt.Errorf("failed to parse trace data: %w", err)
	}

	// 3. Define structs to hold our simplified graph data.
	// This matches what the frontend JavaScript expects.
	type Node struct {
		ID    uint64 `json:"id"`
		Label string `json:"label"`
		Type  string `json:"type"`
		State string `json:"state"`
	}
	type Edge struct {
		From uint64 `json:"from"`
		To   uint64 `json:"to"`
	}
	type Graph struct {
		Nodes []Node `json:"nodes"`
		Edges []Edge `json:"edges"`
	}

	graph := Graph{
		Nodes: make([]Node, 0),
		Edges: make([]Edge, 0),
	}

	// 4. Loop through all events and build our graph structure.
	goroutines := make(map[uint64]*Node)
	// Ensure the main goroutine (ID 1) always exists.
	goroutines[1] = &Node{ID: 1, Label: "goroutine 1 (main)", Type: "goroutine", State: "running"}

	for _, ev := range result.Events {
		switch ev.Type {
		case trace.EvGoCreate:
			// A new goroutine was created.
			childID := ev.Args[0]
			parentID := ev.G
			if _, ok := goroutines[parentID]; !ok {
				goroutines[parentID] = &Node{ID: parentID, Label: fmt.Sprintf("goroutine %d", parentID), Type: "goroutine", State: "created"}
			}
			if _, ok := goroutines[childID]; !ok {
				goroutines[childID] = &Node{ID: childID, Label: fmt.Sprintf("goroutine %d", childID), Type: "goroutine", State: "created"}
			}
			graph.Edges = append(graph.Edges, Edge{From: parentID, To: childID})

		case trace.EvGoStart:
			// A goroutine started running.
			if g, ok := goroutines[ev.G]; ok {
				g.State = "running"
			}
		case trace.EvGoEnd:
			// A goroutine finished.
			if g, ok := goroutines[ev.G]; ok {
				g.State = "finished"
			}
		}
	}

	// 5. Add all found goroutines to the final nodes list.
	for _, node := range goroutines {
		graph.Nodes = append(graph.Nodes, *node)
	}

	// 6. Wrap the graph in a map to match the frontend's expectation.
	// The frontend JavaScript's `convertTraceToGraph` function is no longer needed
	// because we are doing the conversion here on the backend.
	return map[string]interface{}{
		"trace": graph, // The key is "trace", but the value is our new Graph struct
	}, nil
}
