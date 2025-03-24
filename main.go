package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// DeployRequest represents the JSON payload for deployments.
type DeployRequest struct {
	RepoURL string `json:"repo_url"`
}

// Helper function for running shell commands with a timeout
func runCommand(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
	// Create a context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Set the context to the command
	cmd = cmd.WithContext(ctx)

	// Run the command and capture the output
	output, err := cmd.CombinedOutput()
	return output, err
}

func deployHandler(w http.ResponseWriter, r *http.Request) {
	// Parse JSON payload
	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	// Generate a unique folder for this build (e.g., using a timestamp)
	timestamp := time.Now().UnixNano()
	buildFolder := fmt.Sprintf("build-%d", timestamp)
	if err := os.Mkdir(buildFolder, 0755); err != nil {
		http.Error(w, "Failed to create build directory", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(buildFolder) // Cleanup build folder after process is done

	// Clone the repository into the build folder
	cloneCmd := exec.Command("git", "clone", req.RepoURL, buildFolder)
	cloneOutput, err := runCommand(cloneCmd, 5*time.Minute) // 5-minute timeout
	if err != nil {
		log.Printf("Git clone error: %s", string(cloneOutput))
		http.Error(w, "Failed to clone repository", http.StatusInternalServerError)
		return
	}

	// Run "npm install" in the cloned directory
	npmInstallCmd := exec.Command("npm", "install")
	npmInstallCmd.Dir = buildFolder
	installOutput, err := runCommand(npmInstallCmd, 10*time.Minute) // 10-minute timeout
	if err != nil {
		log.Printf("npm install error: %s", string(installOutput))
		http.Error(w, "npm install failed", http.StatusInternalServerError)
		return
	}

	// Run "npm run build" in the cloned directory
	npmBuildCmd := exec.Command("npm", "run", "build")
	npmBuildCmd.Dir = buildFolder
	buildOutput, err := runCommand(npmBuildCmd, 10*time.Minute) // 10-minute timeout
	if err != nil {
		log.Printf("npm build error: %s", string(buildOutput))
		http.Error(w, "npm build failed", http.StatusInternalServerError)
		return
	}

	// Check that the build output folder exists (assuming it's named "dist")
	distPath := filepath.Join(buildFolder, "dist")
	if _, err := os.Stat(distPath); os.IsNotExist(err) {
		http.Error(w, "Build output folder not found", http.StatusInternalServerError)
		return
	}

	// Return success response
	response := map[string]string{
		"message": "Build succeeded",
		"build":   buildFolder,
		"dist":    distPath,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func main() {
	// API endpoint for deployment
	http.HandleFunc("/deploy", deployHandler)

	// Serve static files from "dist" directory (after build)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	log.Println("Server starting on port 8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
