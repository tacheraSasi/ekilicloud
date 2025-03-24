package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type DeployRequest struct {
	RepoURL   string `json:"repo_url"`
	Framework string `json:"framework"`
}

type Deployment struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	Timestamp  time.Time `json:"timestamp"`
	RepoURL    string    `json:"repo_url"`
	Framework  string    `json:"framework"`
	DeployPath string    `json:"deploy_path"`
	Outputs    struct {
		Clone   string `json:"clone"`
		Install string `json:"install"`
		Build   string `json:"build"`
	} `json:"outputs"`
	Error string `json:"error,omitempty"`
}

var (
	deployMutex sync.Mutex
	validFrameworks = map[string]bool{
		"react":    true,
		"vue":      true,
		"angular":  true,
		"svelte":   true,
		"nextjs":   true,
		"nuxt":     true,
	}
)

func runCommand(ctx context.Context, dir string, timeout time.Duration, name string, arg ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, name, arg...)
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if cmdCtx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf("command timed out after %v", timeout)
	}

	return string(output), err
}

func copyDirContents(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip root directory
		if path == src {
			return nil
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		destFile, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, srcFile)
		return err
	})
}

func deployHandler(w http.ResponseWriter, r *http.Request) {
	deployMutex.Lock()
	defer deployMutex.Unlock()

	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if !validFrameworks[req.Framework] {
		http.Error(w, "Invalid framework", http.StatusBadRequest)
		return
	}

	deployment := &Deployment{
		ID:        time.Now().Format("20060102-150405"),
		Status:    "started",
		Timestamp: time.Now(),
		RepoURL:   req.RepoURL,
		Framework: req.Framework,
	}

	defer func() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deployment)
	}()

	// Create temp directory
	tempDir, err := os.MkdirTemp("", "build-")
	if err != nil {
		deployment.Status = "failed"
		deployment.Error = "Failed to create temp directory: " + err.Error()
		return
	}
	defer os.RemoveAll(tempDir)

	// Clone repository
	ctx := context.Background()
	output, err := runCommand(ctx, "", 5*time.Minute, "git", "clone", req.RepoURL, tempDir)
	deployment.Outputs.Clone = output
	if err != nil {
		deployment.Status = "failed"
		deployment.Error = "Clone failed: " + err.Error()
		return
	}

	// Install dependencies
	output, err = runCommand(ctx, tempDir, 10*time.Minute, "npm", "install")
	deployment.Outputs.Install = output
	if err != nil {
		deployment.Status = "failed"
		deployment.Error = "npm install failed: " + err.Error()
		return
	}

	// Build project
	output, err = runCommand(ctx, tempDir, 10*time.Minute, "npm", "run", "build")
	deployment.Outputs.Build = output
	if err != nil {
		deployment.Status = "failed"
		deployment.Error = "npm build failed: " + err.Error()
		return
	}

	// Verify build output
	distPath := filepath.Join(tempDir, "dist")
	if _, err := os.Stat(distPath); err != nil {
		deployment.Status = "failed"
		deployment.Error = "Build output missing: " + err.Error()
		return
	}

	// Create deployment directory
	deployDir := filepath.Join("static", "deployments", deployment.ID)
	if err := os.MkdirAll(deployDir, 0755); err != nil {
		deployment.Status = "failed"
		deployment.Error = "Failed to create deployment directory: " + err.Error()
		return
	}

	// Copy only dist contents
	if err := copyDirContents(distPath, deployDir); err != nil {
		deployment.Status = "failed"
		deployment.Error = "Failed to copy build output: " + err.Error()
		return
	}

	deployment.Status = "success"
	deployment.DeployPath = "/deployments/" + deployment.ID + "/"
}

func main() {
	// Create required directories
	os.MkdirAll(filepath.Join("static", "deployments"), 0755)

	// Serve deployment files
	http.Handle("/deployments/", 
		http.StripPrefix("/deployments/",
			http.FileServer(
				http.Dir(filepath.Join("static", "deployments")))))

	// Deployment endpoint
	http.HandleFunc("/deploy", deployHandler)

	// Start server
	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}