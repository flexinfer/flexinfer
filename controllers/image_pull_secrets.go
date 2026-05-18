package controllers

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// ParseModelImagePullSecrets parses a comma-separated secret name list for
// model pod imagePullSecrets.
func ParseModelImagePullSecrets(raw string) []corev1.LocalObjectReference {
	var refs []corev1.LocalObjectReference
	seen := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		refs = append(refs, corev1.LocalObjectReference{Name: name})
	}
	return refs
}
