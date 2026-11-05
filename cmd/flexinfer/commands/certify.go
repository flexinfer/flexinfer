/*
Copyright 2026.

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
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/flexinfer/flexinfer/pkg/certify"
)

var (
	certifyOpts   = certify.DefaultOptions()
	certifyOutput string
	certifyCheck  bool
)

var certifyCmd = &cobra.Command{
	Use:   "certify",
	Short: "Render the public certification matrix",
	Long: `Render the certification matrix: what flexinfer serves on which GPU class,
at what support level, with the evidence pointer and date behind each claim.

The matrix is generated offline from repository files, so CI can render it
without cluster access: GPUProfile manifests (declared backend posture and
backend-capability certificates), the lane manifests (Model CRs and their
flexinfer.ai/promotion-evidence annotations), the Promotion Matrix table of
.loom/60-validation-matrix.md, and the chart values' runtime.profiles (node to
GPU class). Schema and derivation rules: docs/specs/certification-matrix.md.

Examples:
  # Regenerate the checked-in copy (same as make certify-matrix)
  flexinfer certify render

  # Fail when docs/certification-matrix.json is stale (CI)
  flexinfer certify render --check

  # Print to stdout
  flexinfer certify render -o -`,
}

var certifyRenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render docs/certification-matrix.json from the repository manifests",
	Args:  cobra.NoArgs,
	RunE:  runCertifyRender,
}

func init() {
	f := certifyRenderCmd.Flags()
	f.StringVar(&certifyOpts.Root, "root", certifyOpts.Root, "repository root; every other path is relative to it")
	f.StringVar(&certifyOpts.GPUProfiles, "gpuprofiles", certifyOpts.GPUProfiles, "glob of GPUProfile manifests")
	f.StringVar(&certifyOpts.Models, "models", certifyOpts.Models, "glob of lane (Model) manifests")
	f.StringVar(&certifyOpts.ValidationMatrix, "validation-matrix", certifyOpts.ValidationMatrix, "validation matrix markdown with the Promotion Matrix table")
	f.StringVar(&certifyOpts.NodeClasses, "node-classes", certifyOpts.NodeClasses, "chart values file whose runtime.profiles map nodes to GPU classes")
	f.StringVarP(&certifyOutput, "output", "o", "docs/certification-matrix.json", "output file, relative to --root; - writes to stdout")
	f.BoolVar(&certifyCheck, "check", false, "compare the rendered matrix with --output instead of writing it; exit non-zero when it differs")
	certifyCmd.AddCommand(certifyRenderCmd)
}

func runCertifyRender(cmd *cobra.Command, _ []string) error {
	m, err := certify.Render(certifyOpts)
	if err != nil {
		return err
	}
	out, err := m.Marshal()
	if err != nil {
		return err
	}
	if certifyOutput == "-" {
		_, err = cmd.OutOrStdout().Write(out)
		return err
	}
	path := filepath.Join(certifyOpts.Root, certifyOutput)
	if certifyCheck {
		if err := certify.Check(out, path); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s is current (%d entries)\n", certifyOutput, len(m.Entries))
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d entries)\n", certifyOutput, len(m.Entries))
	return err
}
