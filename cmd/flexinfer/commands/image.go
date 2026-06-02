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
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/registry"
)

var (
	imagePinUsername string
	imagePinPassword string
	imagePinInsecure bool
	imagePinQuiet    bool
)

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Inspect and pin model backend images",
}

var imagePinCmd = &cobra.Command{
	Use:   "pin <image-ref>",
	Short: "Resolve an image tag to its immutable digest",
	Long: `Resolve a container image tag to its registry content digest so it can be
pinned via a Model's spec.imageDigest for reproducible deployments.

Credentials may be supplied via --username/--password or the
FLEXINFER_REGISTRY_USERNAME / FLEXINFER_REGISTRY_PASSWORD environment variables.
Anonymous access and the bearer-token challenge flow (Docker Hub) are supported.

Examples:
  # Resolve a Harbor tag (anonymous or basic auth)
  flexinfer image pin registry.harbor.lan/flexinfer/runtime:master

  # Print only the digest (for scripting / kubectl patch)
  flexinfer image pin nginx:1.25 --quiet`,
	Args: cobra.ExactArgs(1),
	RunE: runImagePin,
}

func runImagePin(cmd *cobra.Command, args []string) error {
	ref := args[0]

	username := imagePinUsername
	if username == "" {
		username = os.Getenv("FLEXINFER_REGISTRY_USERNAME")
	}
	password := imagePinPassword
	if password == "" {
		password = os.Getenv("FLEXINFER_REGISTRY_PASSWORD")
	}

	auth := registry.ImageAuth{Username: username, Password: password, Insecure: imagePinInsecure}
	client := &http.Client{Timeout: 30 * time.Second}

	digest, err := registry.ResolveImageDigest(cmd.Context(), client, ref, auth)
	if err != nil {
		return fmt.Errorf("resolve digest for %q: %w", ref, err)
	}

	out := cmd.OutOrStdout()
	if imagePinQuiet {
		_, _ = fmt.Fprintln(out, digest)
		return nil
	}
	_, _ = fmt.Fprintln(out, backend.ApplyImageDigest(ref, digest))
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nPin reproducibly on the Model CR:\n  spec:\n    imageDigest: %s\n", digest)
	return nil
}

func init() {
	imagePinCmd.Flags().StringVar(&imagePinUsername, "username", "", "registry username (or FLEXINFER_REGISTRY_USERNAME)")
	imagePinCmd.Flags().StringVar(&imagePinPassword, "password", "", "registry password (or FLEXINFER_REGISTRY_PASSWORD)")
	imagePinCmd.Flags().BoolVar(&imagePinInsecure, "insecure", false, "use http instead of https (local/test registries)")
	imagePinCmd.Flags().BoolVar(&imagePinQuiet, "quiet", false, "print only the resolved digest")
	imageCmd.AddCommand(imagePinCmd)
}
