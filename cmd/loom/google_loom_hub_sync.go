package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/pkg/googleworkspace"
	"github.com/crb2nu/loom/pkg/secrets"
)

const (
	defaultLoomHubNamespace              = "loom-hub"
	defaultLoomHubSecretName             = "loom-secrets"
	defaultGoogleWorkspaceDeploymentName = "google-workspace"
	loomHubSecretsRelativePath           = "k3s/loom-hub/secrets.yaml"
)

var googleWorkspaceSecretKeys = []string{
	googleworkspace.SecretClientID,
	googleworkspace.SecretClientSecret,
	googleworkspace.SecretRefreshToken,
	googleworkspace.SecretScopes,
	googleworkspace.SecretAccountEmail,
}

type googleWorkspaceLoomHubSyncOptions struct {
	GitOpsDir         string
	Namespace         string
	SecretName        string
	DeploymentName    string
	SkipGitOps        bool
	SkipCluster       bool
	RestartDeployment bool
	WaitForRollout    bool
	WaitTimeout       time.Duration
}

type googleWorkspaceLoomHubSyncResult struct {
	GitOpsUpdated   bool
	ClusterPatched  bool
	DeploymentReset bool
}

func defaultGoogleWorkspaceLoomHubSyncOptions() googleWorkspaceLoomHubSyncOptions {
	return googleWorkspaceLoomHubSyncOptions{
		GitOpsDir:         resolveDefaultGitOpsDir(),
		Namespace:         defaultLoomHubNamespace,
		SecretName:        defaultLoomHubSecretName,
		DeploymentName:    defaultGoogleWorkspaceDeploymentName,
		RestartDeployment: true,
		WaitForRollout:    true,
		WaitTimeout:       3 * time.Minute,
	}
}

func resolveDefaultGitOpsDir() string {
	if dir := strings.TrimSpace(os.Getenv("LOOM_GITOPS_DIR")); dir != "" {
		return dir
	}
	if workspaceRoot := findWorkspaceRootForChecks(); workspaceRoot != "" {
		candidate := filepath.Join(workspaceRoot, "platform", "gitops")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		candidate := filepath.Join(home, "workspace", "platform", "gitops")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func syncGoogleWorkspaceLoomHub(ctx context.Context, mgr *secrets.Manager, opts googleWorkspaceLoomHubSyncOptions) (*googleWorkspaceLoomHubSyncResult, error) {
	if mgr == nil {
		return nil, fmt.Errorf("secret manager is required")
	}
	values := googleWorkspaceSecretValues(mgr)
	result := &googleWorkspaceLoomHubSyncResult{}

	if !opts.SkipGitOps {
		if strings.TrimSpace(opts.GitOpsDir) == "" {
			return nil, fmt.Errorf("platform/gitops repository not found (set LOOM_GITOPS_DIR or use --gitops-dir)")
		}
		changed, err := updateEncryptedLoomHubSecretFile(ctx, opts.GitOpsDir, values)
		if err != nil {
			return nil, err
		}
		result.GitOpsUpdated = changed
	}

	clusterChanged := false
	if !opts.SkipCluster {
		changed, err := patchLoomHubSecret(ctx, opts.Namespace, opts.SecretName, values)
		if err != nil {
			return nil, err
		}
		clusterChanged = changed
		result.ClusterPatched = changed
	}

	if opts.RestartDeployment && !opts.SkipCluster && (clusterChanged || result.GitOpsUpdated) {
		if err := rolloutRestartDeployment(ctx, opts.Namespace, opts.DeploymentName, opts.WaitForRollout, opts.WaitTimeout); err != nil {
			return nil, err
		}
		result.DeploymentReset = true
	}

	return result, nil
}

func googleWorkspaceSecretValues(mgr *secrets.Manager) map[string]string {
	values := make(map[string]string, len(googleWorkspaceSecretKeys))
	for _, key := range googleWorkspaceSecretKeys {
		values[key] = strings.TrimSpace(mgr.GetValue(key))
	}
	return values
}

func updateEncryptedLoomHubSecretFile(ctx context.Context, gitOpsDir string, values map[string]string) (bool, error) {
	if _, err := exec.LookPath("sops"); err != nil {
		return false, fmt.Errorf("sops is required to sync GitOps secrets: %w", err)
	}

	secretPath := filepath.Join(gitOpsDir, loomHubSecretsRelativePath)
	if _, err := os.Stat(secretPath); err != nil {
		return false, fmt.Errorf("loom-hub secret file not found: %w", err)
	}

	decrypted, err := runCommand(ctx, gitOpsDir, "sops", "decrypt", loomHubSecretsRelativePath)
	if err != nil {
		return false, fmt.Errorf("decrypt loom-hub secret: %w", err)
	}

	updated, changed, err := upsertStringDataYAML([]byte(decrypted), values, googleWorkspaceSecretKeys)
	if err != nil {
		return false, fmt.Errorf("update loom-hub secret YAML: %w", err)
	}
	if !changed {
		return false, nil
	}

	tempDir := filepath.Dir(secretPath)
	tmp, err := os.CreateTemp(tempDir, ".google-workspace-secrets-*.yaml")
	if err != nil {
		return false, fmt.Errorf("create temporary secret file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(updated); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("write temporary secret file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close temporary secret file: %w", err)
	}

	relTemp, err := filepath.Rel(gitOpsDir, tmpPath)
	if err != nil {
		return false, fmt.Errorf("compute temporary secret path: %w", err)
	}
	encrypted, err := runCommand(ctx, gitOpsDir, "sops", "encrypt", "--filename-override", loomHubSecretsRelativePath, relTemp)
	if err != nil {
		return false, fmt.Errorf("encrypt loom-hub secret: %w", err)
	}
	if err := os.WriteFile(secretPath, []byte(encrypted), 0600); err != nil {
		return false, fmt.Errorf("write encrypted loom-hub secret: %w", err)
	}
	return true, nil
}

func upsertStringDataYAML(input []byte, values map[string]string, keyOrder []string) ([]byte, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(input, &root); err != nil {
		return nil, false, err
	}
	if len(root.Content) == 0 {
		return nil, false, fmt.Errorf("yaml document is empty")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("yaml document root must be a mapping")
	}

	stringData := ensureMappingValue(doc, "stringData")
	if stringData.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("stringData must be a mapping")
	}

	changed := false
	for _, key := range keyOrder {
		value := values[key]
		if upsertMappingScalar(stringData, key, value) {
			changed = true
		}
	}
	if !changed {
		return nil, false, nil
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&root); err != nil {
		return nil, false, err
	}
	if err := enc.Close(); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), true, nil
}

func ensureMappingValue(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valueNode)
	return valueNode
}

func upsertMappingScalar(mapping *yaml.Node, key, value string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		if mapping.Content[i+1].Value == value {
			return false
		}
		mapping.Content[i+1].Kind = yaml.ScalarNode
		mapping.Content[i+1].Tag = "!!str"
		mapping.Content[i+1].Value = value
		return true
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return true
}

func patchLoomHubSecret(ctx context.Context, namespace, secretName string, values map[string]string) (bool, error) {
	liveValues, err := fetchClusterSecretValues(ctx, namespace, secretName, googleWorkspaceSecretKeys)
	if err != nil {
		return false, err
	}
	if sameStringValues(liveValues, values, googleWorkspaceSecretKeys) {
		return false, nil
	}

	data := make(map[string]string, len(googleWorkspaceSecretKeys))
	for _, key := range googleWorkspaceSecretKeys {
		data[key] = base64.StdEncoding.EncodeToString([]byte(values[key]))
	}
	patch := map[string]any{"data": data}
	body, err := json.Marshal(patch)
	if err != nil {
		return false, fmt.Errorf("marshal secret patch: %w", err)
	}
	if _, err := runCommand(ctx, "", "kubectl", "-n", namespace, "patch", "secret", secretName, "--type", "merge", "-p", string(body)); err != nil {
		return false, fmt.Errorf("patch %s/%s: %w", namespace, secretName, err)
	}
	return true, nil
}

func fetchClusterSecretValues(ctx context.Context, namespace, secretName string, keys []string) (map[string]string, error) {
	output, err := runCommand(ctx, "", "kubectl", "-n", namespace, "get", "secret", secretName, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("get %s/%s: %w", namespace, secretName, err)
	}
	var payload struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, fmt.Errorf("parse secret JSON: %w", err)
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		decoded, err := base64.StdEncoding.DecodeString(payload.Data[key])
		if err != nil {
			return nil, fmt.Errorf("decode secret key %s: %w", key, err)
		}
		values[key] = string(decoded)
	}
	return values, nil
}

func sameStringValues(left, right map[string]string, keys []string) bool {
	for _, key := range keys {
		if left[key] != right[key] {
			return false
		}
	}
	return true
}

func rolloutRestartDeployment(ctx context.Context, namespace, deployment string, wait bool, timeout time.Duration) error {
	if _, err := runCommand(ctx, "", "kubectl", "-n", namespace, "rollout", "restart", "deployment/"+deployment); err != nil {
		return fmt.Errorf("rollout restart deployment/%s: %w", deployment, err)
	}
	if !wait {
		return nil
	}
	timeoutValue := timeout
	if timeoutValue <= 0 {
		timeoutValue = 3 * time.Minute
	}
	if _, err := runCommand(ctx, "", "kubectl", "-n", namespace, "rollout", "status", "deployment/"+deployment, "--timeout", timeoutValue.String()); err != nil {
		return fmt.Errorf("rollout status deployment/%s: %w", deployment, err)
	}
	return nil
}

func runCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}
