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
		Prisma  string `json:"prisma,omitempty"`
	} `json:"outputs"`
	Error string `json:"error,omitempty"`
}

var (
	deployMutex sync.Mutex
	validFrameworks = map[string]bool{
		"react":       true,
		"vue":        true,
		"angular":    true,
		"svelte":     true,
		"node-prisma": true,
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

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
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

		if _, err := io.Copy(destFile, srcFile); err != nil {
			return err
		}

		return os.Chmod(destPath, info.Mode())
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

	buildPath := filepath.Join("static", "deployments", deployment.ID)
	deployPath := filepath.Join(buildPath, "dist")
	deployment.DeployPath = "/deployments/" + deployment.ID + "/dist/"

	// Create build directory
	if err := os.MkdirAll(buildPath, 0755); err != nil {
		deployment.Status = "failed"
		deployment.Error = "Failed to create build directory: " + err.Error()
		return
	}

	// Clone repository
	ctx := context.Background()
	output, err := runCommand(ctx, "", 5*time.Minute, "git", "clone", req.RepoURL, buildPath)
	deployment.Outputs.Clone = output
	if err != nil {
		deployment.Status = "failed"
		deployment.Error = "Clone failed: " + err.Error()
		return
	}

	// Framework-specific handling
	if req.Framework == "node-prisma" {
		if _, err := os.Stat(filepath.Join(buildPath, "prisma/schema.prisma")); err == nil {
			output, err = runCommand(ctx, buildPath, 2*time.Minute, "npx", "prisma", "generate")
			deployment.Outputs.Prisma = output
			if err != nil {
				deployment.Status = "failed"
				deployment.Error = "Prisma generate failed: " + err.Error()
				return
			}

			output, err = runCommand(ctx, buildPath, 2*time.Minute, "npx", "prisma", "migrate", "deploy")
			deployment.Outputs.Prisma += "\n" + output
			if err != nil {
				deployment.Status = "failed"
				deployment.Error = "Prisma migrate failed: " + err.Error()
				return
			}
		}
	}

	// Install dependencies
	output, err = runCommand(ctx, buildPath, 10*time.Minute, "npm", "install")
	deployment.Outputs.Install = output
	if err != nil {
		deployment.Status = "failed"
		deployment.Error = "npm install failed: " + err.Error()
		return
	}

	// Build project
	output, err = runCommand(ctx, buildPath, 10*time.Minute, "npm", "run", "build")
	deployment.Outputs.Build = output
	if err != nil {
		deployment.Status = "failed"
		deployment.Error = "npm build failed: " + err.Error()
		return
	}

	// Verify build output
	if _, err := os.Stat(deployPath); err != nil {
		deployment.Status = "failed"
		deployment.Error = "Build output missing: " + err.Error()
		return
	}

	deployment.Status = "success"
}

func main() {
	// Create required directories
	// if _, err := os.Stat("static"); os.IsNotExist(err) {
	// 		os.Mkdir("static", 0755)
	// }
	os.MkdirAll(filepath.Join("static", "deployments"), 0755)

	// Serve deployment files
	http.Handle("/deployments/", http.StripPrefix("/deployments/",
		http.FileServer(http.Dir(filepath.Join("static", "deployments"))))

	// Deployment endpoint
	http.HandleFunc("/deploy", deployHandler)

	// Start server
	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
