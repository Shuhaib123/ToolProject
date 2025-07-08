package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
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

func main() {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		fmt.Println("Could not determine Go version:", err)
	} else {
		fmt.Println("Go version used by exec.Command:", string(out))
	}
	http.HandleFunc("/trace", withCORS(traceHandler))
	fmt.Println("Go backend listening on :8080 (POST /trace)")
	http.ListenAndServe(":8080", nil)
}

func traceHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Read the Go code from the request body
		code, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read code", 400)
			return
		}
		fmt.Println("Received code:\n", string(code))

		// 2. Create temporary working directory
		dir, _ := ioutil.TempDir("", "gtrace")
		// defer os.RemoveAll(dir) // Uncomment this to auto-clean after testing
		tmpFile := filepath.Join(dir, "main.go")
		err = ioutil.WriteFile(tmpFile, code, 0644)
		if err != nil {
			http.Error(w, "Failed to write temp file", 500)
			return
		}

	// 3. Run the Go code to generate trace.out
	cmd := exec.Command("go", "run", tmpFile)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	fmt.Println("Go run output:\n", string(output))
	if err != nil {
		fmt.Println("Go run error:", err)
	}

	tracePath := filepath.Join(dir, "trace.out")
	fmt.Println("Trace path:", tracePath)

	// 4. Check if trace.out was generated
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		missingTraceHint := ""
		if !bytes.Contains(code, []byte("trace.Start")) || !bytes.Contains(code, []byte("trace.Stop")) {
			missingTraceHint = "Your code did not include trace.Start/trace.Stop. Please ensure you use the 'Generate Code Wrapper' step before running GTrace."
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "trace.out not generated",
			"go_run_output": string(output),
			"run_error":     missingTraceHint,
		})
		return
	}

	// 5. Parse trace.out into JSON
	jsonData, err := analyzeTrace(tracePath)
	if err != nil {
		fmt.Println("Trace analyze error:", err)
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

// analyzeTrace parses trace.out using `go tool trace -json`
func analyzeTrace(tracePath string) (map[string]interface{}, error) {
	cmd := exec.Command("go", "tool", "trace", "-json", tracePath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		fmt.Println("go tool trace -json failed:", err)
		fmt.Println("trace tool output:", out.String())
		return nil, fmt.Errorf("go tool trace failed: %v", err)
	}

	// Parse JSON output
	var parsed interface{}
	dec := json.NewDecoder(&out)
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("failed to decode trace json: %v", err)
	}

	return map[string]interface{}{
		"trace":     parsed,
		"timestamp": time.Now().Unix(),
	}, nil
}
