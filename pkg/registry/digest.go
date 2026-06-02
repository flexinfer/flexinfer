package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ImageAuth carries optional credentials for resolving a container image digest.
type ImageAuth struct {
	// Username and Password are used for HTTP basic auth and when requesting a
	// bearer token. Both empty means anonymous access.
	Username string
	Password string
	// Insecure uses http instead of https (for local/test registries).
	Insecure bool
}

// dockerHubHost is the registry endpoint used when a reference omits a host.
const dockerHubHost = "registry-1.docker.io"

// manifestAcceptTypes are the manifest media types accepted when resolving a
// digest. Including both Docker v2 schema 2 and OCI manifests plus their
// multi-arch index types makes the registry return the canonical
// Docker-Content-Digest of the addressed tag.
var manifestAcceptTypes = []string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
}

// parsedRef holds the components of a container image reference.
type parsedRef struct {
	Host string // registry host (e.g. registry.harbor.lan, 127.0.0.1:5000)
	Repo string // repository path (e.g. flexinfer/runtime, library/nginx)
	Tag  string // tag (e.g. master, latest)
}

// parseImageRef splits a container image reference into host/repo/tag. A
// reference without an explicit registry host defaults to Docker Hub, applying
// the "library/" prefix for single-segment repositories. References that
// already pin a digest are rejected (there is nothing to resolve).
func parseImageRef(ref string) (parsedRef, error) {
	if ref == "" {
		return parsedRef{}, fmt.Errorf("empty image reference")
	}
	if strings.Contains(ref, "@") {
		return parsedRef{}, fmt.Errorf("reference %q already pins a digest", ref)
	}

	host := ""
	remainder := ref
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		first := ref[:i]
		// A leading segment is a registry host only if it looks like one: it
		// contains a '.' or ':' (domain or host:port) or is "localhost".
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			host = first
			remainder = ref[i+1:]
		}
	}
	if host == "" {
		host = dockerHubHost
	}

	repo := remainder
	tag := "latest"
	// The tag is the part after the last ':' that comes after the last '/', so
	// a registry-host port colon is never mistaken for a tag separator.
	if c := strings.LastIndexByte(remainder, ':'); c > strings.LastIndexByte(remainder, '/') {
		repo = remainder[:c]
		tag = remainder[c+1:]
	}
	if host == dockerHubHost && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	if repo == "" {
		return parsedRef{}, fmt.Errorf("could not parse repository from %q", ref)
	}
	return parsedRef{Host: host, Repo: repo, Tag: tag}, nil
}

func scheme(insecure bool) string {
	if insecure {
		return "http"
	}
	return "https"
}

// ResolveImageDigest performs a Registry v2 manifest HEAD and returns the
// canonical content digest (sha256:...) of the referenced tag. It handles
// anonymous access, HTTP basic auth, and the bearer-token challenge flow used
// by Docker Hub and other token-auth registries. A nil client uses
// http.DefaultClient.
func ResolveImageDigest(ctx context.Context, client *http.Client, ref string, auth ImageAuth) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	p, err := parseImageRef(ref)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme(auth.Insecure), p.Host, p.Repo, p.Tag)

	digest, status, challenge, err := headManifest(ctx, client, url, auth, "")
	if err != nil {
		return "", err
	}
	if status == http.StatusUnauthorized && challenge != "" {
		token, terr := fetchBearerToken(ctx, client, challenge, auth)
		if terr != nil {
			return "", terr
		}
		digest, status, _, err = headManifest(ctx, client, url, auth, token)
		if err != nil {
			return "", err
		}
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("registry returned status %d resolving %q", status, ref)
	}
	if digest == "" {
		return "", fmt.Errorf("registry did not return a Docker-Content-Digest for %q", ref)
	}
	return digest, nil
}

// headManifest issues a HEAD against a manifest URL and returns the
// Docker-Content-Digest header, the status code, and any WWW-Authenticate
// challenge. A bearer token (when non-empty) takes precedence over basic auth.
func headManifest(ctx context.Context, client *http.Client, url string, auth ImageAuth, bearer string) (digest string, status int, challenge string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", 0, "", err
	}
	for _, mt := range manifestAcceptTypes {
		req.Header.Add("Accept", mt)
	}
	switch {
	case bearer != "":
		req.Header.Set("Authorization", "Bearer "+bearer)
	case auth.Username != "":
		req.SetBasicAuth(auth.Username, auth.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Header.Get("Docker-Content-Digest"), resp.StatusCode, resp.Header.Get("WWW-Authenticate"), nil
}

// fetchBearerToken parses a bearer challenge (realm/service/scope) and requests
// a token from the realm endpoint.
func fetchBearerToken(ctx context.Context, client *http.Client, challenge string, auth ImageAuth) (string, error) {
	realm, params := parseBearerChallenge(challenge)
	if realm == "" {
		return "", fmt.Errorf("unsupported auth challenge: %q", challenge)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm, nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	if svc := params["service"]; svc != "" {
		q.Set("service", svc)
	}
	if scope := params["scope"]; scope != "" {
		q.Set("scope", scope)
	}
	req.URL.RawQuery = q.Encode()
	if auth.Username != "" {
		req.SetBasicAuth(auth.Username, auth.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tok.Token != "" {
		return tok.Token, nil
	}
	return tok.AccessToken, nil
}

// parseBearerChallenge extracts the realm and parameters from a
// `Bearer realm="...",service="...",scope="..."` WWW-Authenticate header.
func parseBearerChallenge(h string) (realm string, params map[string]string) {
	params = map[string]string{}
	h = strings.TrimSpace(h)
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return "", params
	}
	for _, part := range splitChallengeParams(h[len("Bearer "):]) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		if key == "realm" {
			realm = val
		} else {
			params[key] = val
		}
	}
	return realm, params
}

// splitChallengeParams splits a challenge parameter list on commas that are not
// inside a quoted value.
func splitChallengeParams(s string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch r {
		case '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case ',':
			if inQuote {
				b.WriteRune(r)
			} else {
				parts = append(parts, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}
