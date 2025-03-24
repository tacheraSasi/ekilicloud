package main

import (
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

	// Clone the repository into the build folder
	cloneCmd := exec.Command("git", "clone", req.RepoURL, buildFolder)
	cloneOutput, err := cloneCmd.CombinedOutput()
	if err != nil {
		log.Printf("Git clone error: %s", string(cloneOutput))
		http.Error(w, "Failed to clone repository", http.StatusInternalServerError)
		return
	}

	// Run "npm install" in the cloned directory
	npmInstallCmd := exec.Command("npm", "install")
	npmInstallCmd.Dir = buildFolder
	installOutput, err := npmInstallCmd.CombinedOutput()
	if err != nil {
		log.Printf("npm install error: %s", string(installOutput))
		http.Error(w, "npm install failed", http.StatusInternalServerError)
		return
	}

	// Run "npm run build" in the cloned directory
	npmBuildCmd := exec.Command("npm", "run", "build")
	npmBuildCmd.Dir = buildFolder
	buildOutput, err := npmBuildCmd.CombinedOutput()
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

	// At this point, you could set up routing for a domain that serves files from 'dist'.
	// For demonstration, we simply return a success message with the path.
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

	// Optionally, serve static files from a fixed directory for testing.
	// In production, you would route domains to the correct "dist" folder.
	// Here, "/static/" serves files from a "static" folder.
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	log.Println("Server starting on port 8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
