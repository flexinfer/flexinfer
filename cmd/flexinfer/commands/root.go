/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var (
	kubeconfig string
	namespace  string
	allNs      bool
	scheme     = runtime.NewScheme()
)

func init() {
	_ = aiv1alpha1.AddToScheme(scheme)
}

var rootCmd = &cobra.Command{
	Use:   "flexinfer",
	Short: "FlexInfer CLI - manage AI model deployments on Kubernetes",
	Long: `FlexInfer CLI provides commands to manage ModelDeployment resources
on your Kubernetes cluster. It supports listing, inspecting, and managing
AI inference workloads.

Examples:
  # List all model deployments
  flexinfer list

  # Get detailed status of a deployment
  flexinfer status qwen3-8b-amd

  # Stream logs from a model
  flexinfer logs qwen3-8b-amd -f`,
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig file (default: ~/.kube/config)")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "flexinfer-system", "Kubernetes namespace")
	rootCmd.PersistentFlags().BoolVarP(&allNs, "all-namespaces", "A", false, "List resources across all namespaces")

	// Add subcommands
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(scaleCmd)
	rootCmd.AddCommand(cacheCmd)
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

// getKubeConfig returns the Kubernetes rest.Config
func getKubeConfig() (*rest.Config, error) {
	if kubeconfig == "" {
		// Try in-cluster config first
		cfg, err := rest.InClusterConfig()
		if err == nil {
			return cfg, nil
		}

		// Fall back to kubeconfig file
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

// getClient returns a controller-runtime client for CRD operations
func getClient() (client.Client, error) {
	cfg, err := getKubeConfig()
	if err != nil {
		return nil, err
	}

	return client.New(cfg, client.Options{Scheme: scheme})
}

// getClientset returns a kubernetes clientset for core operations
func getClientset() (*kubernetes.Clientset, error) {
	cfg, err := getKubeConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(cfg)
}

// getNamespace returns the namespace to use for queries
func getNamespace() string {
	if allNs {
		return ""
	}
	return namespace
}

// ctx returns a context that respects SIGINT/SIGTERM signals.
// This allows CLI commands to be gracefully interrupted.
func ctx() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}
