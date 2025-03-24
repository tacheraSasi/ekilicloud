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

// DeployRequest represents the JSON payload for deployments.
type DeployRequest struct {
	RepoURL string `json:"repo_url"`
}

var (
	deployMutex sync.Mutex
)

// Helper function for running shell commands with a timeout
func runCommand(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error)
	var output []byte

	go func() {
		var err error
		output, err = cmd.CombinedOutput()
		done <- err
	}()

	select {
	case err := <-done:
		return output, err
	case <-ctx.Done():
		cmd.Process.Kill()
		return nil, fmt.Errorf("command timed out after %v", timeout)
	}
}

// copyDir recursively copies a directory
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
			if err := os.MkdirAll(destPath, info.Mode()); err != nil {
				return err
			}
		} else {
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

			if err := os.Chmod(destPath, info.Mode()); err != nil {
				return err
			}
		}
		return nil
	})
}

func deployHandler(w http.ResponseWriter, r *http.Request) {
	deployMutex.Lock()
	defer deployMutex.Unlock()

	// Validate API Key
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != os.Getenv("DEPLOY_API_KEY") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		log.Println("Unauthorized deployment attempt")
		return
	}

	// Parse JSON payload
	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		log.Println("Invalid payload:", err)
		return
	}

	// Validate RepoURL
	if !(len(req.RepoURL) > 3 && req.RepoURL[:4] == "http") {
		http.Error(w, "Invalid repository URL", http.StatusBadRequest)
		log.Println("Invalid RepoURL:", req.RepoURL)
		return
	}

	// Create build directory
	timestamp := time.Now().UnixNano()
	buildFolder := fmt.Sprintf("build-%d", timestamp)
	if err := os.Mkdir(buildFolder, 0755); err != nil {
		http.Error(w, "Failed to create build directory", http.StatusInternalServerError)
		log.Println("Mkdir error:", err)
		return
	}
	defer os.RemoveAll(buildFolder)

	// Clone repository
	cloneCmd := exec.Command("git", "clone", req.RepoURL, buildFolder)
	cloneOutput, err := runCommand(cloneCmd, 5*time.Minute)
	if err != nil {
		log.Printf("Clone failed: %s\nOutput: %s", err, cloneOutput)
		http.Error(w, "Failed to clone repository", http.StatusInternalServerError)
		return
	}

	// Check package.json exists
	if _, err := os.Stat(filepath.Join(buildFolder, "package.json")); os.IsNotExist(err) {
		http.Error(w, "No package.json found", http.StatusBadRequest)
		log.Println("No package.json in repository")
		return
	}

	// npm install
	npmInstallCmd := exec.Command("npm", "install")
	npmInstallCmd.Dir = buildFolder
	installOutput, err := runCommand(npmInstallCmd, 10*time.Minute)
	if err != nil {
		log.Printf("npm install failed: %s\nOutput: %s", err, installOutput)
		http.Error(w, "npm install failed", http.StatusInternalServerError)
		return
	}

	// npm run build
	npmBuildCmd := exec.Command("npm", "run", "build")
	npmBuildCmd.Dir = buildFolder
	buildOutput, err := runCommand(npmBuildCmd, 10*time.Minute)
	if err != nil {
		log.Printf("npm build failed: %s\nOutput: %s", err, buildOutput)
		http.Error(w, "npm build failed", http.StatusInternalServerError)
		return
	}

	// Verify build output
	distPath := filepath.Join(buildFolder, "dist")
	if _, err := os.Stat(distPath); os.IsNotExist(err) {
		http.Error(w, "Build output 'dist' not found", http.StatusInternalServerError)
		log.Println("Build output missing")
		return
	}

	// Prepare new static directory
	newStatic := "static_new"
	os.RemoveAll(newStatic)
	if err := copyDir(distPath, newStatic); err != nil {
		log.Printf("Copy failed: %v", err)
		http.Error(w, "Failed to prepare deployment", http.StatusInternalServerError)
		return
	}

	// Atomic swap
	oldStatic := "static_old"
	os.RemoveAll(oldStatic)
	if err := os.Rename("static", oldStatic); err != nil && !os.IsNotExist(err) {
		log.Printf("Rename static failed: %v", err)
		http.Error(w, "Deployment failed", http.StatusInternalServerError)
		return
	}

	if err := os.Rename(newStatic, "static"); err != nil {
		log.Printf("Atomic swap failed: %v", err)
		// Attempt rollback
		if err := os.Rename(oldStatic, "static"); err != nil {
			log.Printf("Rollback failed: %v", err)
		}
		http.Error(w, "Deployment failed", http.StatusInternalServerError)
		return
	}

	// Cleanup old static
	os.RemoveAll(oldStatic)

	// Respond
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Deployment successful",
	})
	log.Println("Deployment succeeded for repo:", req.RepoURL)
}

func main() {
	// Ensure static directory existsekili
	if _, err := os.Stat("static"); os.IsNotExist(err) {
		os.Mkdir("static", 0755)
	}

	http.HandleFunc("/deploy", deployHandler)
	http.Handle("/", http.FileServer(http.Dir("./static")))

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}