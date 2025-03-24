// pkg/deploy/deployer.go
package deploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yourusername/deployer/pkg/command"
	"github.com/yourusername/deployer/pkg/types"
)

type Deployer struct {
	runner commandRunner
}

type commandRunner func(dir string, timeout time.Duration, name string, args ...string) (string, error)

func NewDeployer(runner commandRunner) *Deployer {
	return &Deployer{runner: runner}
}

func (d *Deployer) Execute(deployment *types.Deployment) *types.DeploymentResponse {
	defer func() {
		if r := recover(); r != nil {
			deployment.Error = fmt.Sprintf("Panic: %v", r)
			deployment.Status = "failed"
		}
	}()

	// Create build directory
	if err := os.MkdirAll(deployment.BuildDir, 0755); err != nil {
		return d.fail(deployment, "Failed to create directory", err)
	}

	// Clone repository
	output, err := d.runner("", 5*time.Minute, "git", "clone", deployment.RepoURL, deployment.BuildDir)
	deployment.Outputs.Clone = output
	if err != nil {
		return d.fail(deployment, "Clone failed", err)
	}

	// Framework-specific setup
	switch deployment.Framework {
	case "node-prisma":
		if err := d.handlePrisma(deployment); err != nil {
			return d.fail(deployment, "Prisma setup failed", err)
		}
	}

	// Install dependencies
	output, err = d.runner(deployment.BuildDir, 10*time.Minute, "npm", "install")
	deployment.Outputs.Install = output
	if err != nil {
		return d.fail(deployment, "npm install failed", err)
	}

	// Build project
	output, err = d.runner(deployment.BuildDir, 10*time.Minute, "npm", "run", "build")
	deployment.Outputs.Build = output
	if err != nil {
		return d.fail(deployment, "npm build failed", err)
	}

	// Verify build output
	if _, err := os.Stat(filepath.Join(deployment.BuildDir, "dist")); err != nil {
		return d.fail(deployment, "Build output missing", err)
	}

	deployment.Status = "success"
	deployment.DeployPath = "/deployments/" + deployment.ID + "/dist/"
	return &types.DeploymentResponse{Deployment: deployment}
}

func (d *Deployer) handlePrisma(deployment *types.Deployment) error {
	// Check for prisma schema
	if _, err := os.Stat(filepath.Join(deployment.BuildDir, "prisma/schema.prisma")); err != nil {
		return nil // No prisma needed
	}

	// Run prisma commands
	output, err := d.runner(deployment.BuildDir, 2*time.Minute, "npx", "prisma", "generate")
	deployment.Outputs.Prisma = output
	if err != nil {
		return err
	}

	output, err = d.runner(deployment.BuildDir, 2*time.Minute, "npx", "prisma", "migrate", "deploy")
	deployment.Outputs.Prisma += "\n" + output
	return err
}

func (d *Deployer) fail(deployment *types.Deployment, message string, err error) *types.DeploymentResponse {
	deployment.Status = "failed"
	deployment.Error = fmt.Sprintf("%s: %v", message, err)
	return &types.DeploymentResponse{Deployment: deployment}
}