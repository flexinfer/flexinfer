package proxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Job storage: where a job's stored inputs and its output live. The owner
// picks the backend (decision O2 of the 2026-10-31 design): MinIO
// (daemon/minio, s3Store) or a Longhorn PVC mounted into the proxy (pvcStore).
// Both are addressed by key; the URI a store returns is what InferenceJob
// status records and what Get and Delete take back.

// JobStore keeps job objects.
type JobStore interface {
	// Put stores size bytes from body under key; sha256 is their hex digest.
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64, sha256, contentType string) (uri string, err error)
	// Get opens the object at uri.
	Get(ctx context.Context, uri string) (io.ReadCloser, error)
	// Delete removes the object at uri; a missing object is not an error.
	Delete(ctx context.Context, uri string) error
}

// errJobObjectNotFound is returned by Get for a missing object.
var errJobObjectNotFound = errors.New("job object not found")

// jobObjectKey is the storage key of one object of a job.
func jobObjectKey(namespace, job, object string) string {
	return "jobs/" + namespace + "/" + job + "/" + object
}

// ── PVC ──────────────────────────────────────────────────────────

// pvcStore keeps objects as files under root, a PVC mounted into the proxy.
// URIs are pvc://<claim>/<key>.
type pvcStore struct {
	root  string
	claim string
}

func newPVCStore(root, claim string) (*pvcStore, error) {
	if root == "" {
		return nil, errors.New("pvc job store: no mount path")
	}
	if claim == "" {
		claim = "jobs"
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("pvc job store: %w", err)
	}
	return &pvcStore{root: root, claim: claim}, nil
}

func (s *pvcStore) Put(_ context.Context, key string, body io.ReadSeeker, _ int64, _, _ string) (string, error) {
	path, err := s.path(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".put-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return "pvc://" + s.claim + "/" + key, nil
}

func (s *pvcStore) Get(_ context.Context, uri string) (io.ReadCloser, error) {
	path, err := s.uriPath(uri)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errJobObjectNotFound
	}
	return f, err
}

func (s *pvcStore) Delete(_ context.Context, uri string) error {
	path, err := s.uriPath(uri)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(filepath.Dir(path)) // the job's directory, once empty
	return nil
}

func (s *pvcStore) uriPath(uri string) (string, error) {
	key, ok := strings.CutPrefix(uri, "pvc://"+s.claim+"/")
	if !ok {
		return "", fmt.Errorf("pvc job store: %q is not in claim %s", uri, s.claim)
	}
	return s.path(key)
}

// path maps key below root, refusing anything that would escape it.
func (s *pvcStore) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if clean == "/" || strings.Contains(key, "..") {
		return "", fmt.Errorf("pvc job store: bad key %q", key)
	}
	return filepath.Join(s.root, clean), nil
}

// ── S3 / MinIO ───────────────────────────────────────────────────

// emptySHA256 is the hex SHA-256 of an empty payload.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// s3Store keeps objects in an S3-compatible bucket (MinIO) with path-style
// addressing and AWS Signature V4. URIs are s3://<bucket>/<key>.
type s3Store struct {
	endpoint  *url.URL
	bucket    string
	region    string
	accessKey string
	secretKey string
	client    *http.Client
	now       func() time.Time
}

func newS3Store(endpoint, bucket, region, accessKey, secretKey string) (*s3Store, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("s3 job store: bad endpoint %q", endpoint)
	}
	if bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("s3 job store: bucket and credentials are required")
	}
	if region == "" {
		region = "us-east-1"
	}
	return &s3Store{
		endpoint: u, bucket: bucket, region: region, accessKey: accessKey, secretKey: secretKey,
		client: &http.Client{Timeout: 30 * time.Minute}, now: time.Now,
	}, nil
}

func (s *s3Store) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, sum, contentType string) (string, error) {
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	req, err := s.request(ctx, http.MethodPut, key, io.NopCloser(body), sum)
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("s3 put %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("s3 put %s: status %d: %s", key, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return "s3://" + s.bucket + "/" + key, nil
}

func (s *s3Store) Get(ctx context.Context, uri string) (io.ReadCloser, error) {
	key, err := s.key(uri)
	if err != nil {
		return nil, err
	}
	req, err := s.request(ctx, http.MethodGet, key, nil, emptySHA256)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, errJobObjectNotFound
	}
	if resp.StatusCode/100 != 2 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("s3 get %s: status %d", key, resp.StatusCode)
	}
	return resp.Body, nil
}

func (s *s3Store) Delete(ctx context.Context, uri string) error {
	key, err := s.key(uri)
	if err != nil {
		return err
	}
	req, err := s.request(ctx, http.MethodDelete, key, nil, emptySHA256)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("s3 delete %s: status %d", key, resp.StatusCode)
	}
	return nil
}

func (s *s3Store) key(uri string) (string, error) {
	key, ok := strings.CutPrefix(uri, "s3://"+s.bucket+"/")
	if !ok || key == "" {
		return "", fmt.Errorf("s3 job store: %q is not in bucket %s", uri, s.bucket)
	}
	return key, nil
}

// request builds a signed path-style request for bucket/key.
func (s *s3Store) request(ctx context.Context, method, key string, body io.ReadCloser, payloadSHA256 string) (*http.Request, error) {
	canonicalURI := "/" + s3EscapePath(s.bucket+"/"+key)
	if p := strings.TrimRight(s.endpoint.Path, "/"); p != "" {
		canonicalURI = s3EscapePath(p) + canonicalURI
	}
	req, err := http.NewRequestWithContext(ctx, method, s.endpoint.Scheme+"://"+s.endpoint.Host+canonicalURI, body)
	if err != nil {
		return nil, err
	}
	amzDate := s.now().UTC().Format("20060102T150405Z")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadSHA256)
	req.Header.Set("Authorization", signV4(method, canonicalURI, "", map[string]string{
		"host":                 s.endpoint.Host,
		"x-amz-content-sha256": payloadSHA256,
		"x-amz-date":           amzDate,
	}, payloadSHA256, s.accessKey, s.secretKey, s.region, "s3", amzDate))
	return req, nil
}

// s3EscapePath URI-encodes each path segment the way SigV4 canonicalizes S3
// paths: everything but unreserved characters, keeping '/'.
func s3EscapePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// signV4 returns the Authorization header value for an AWS Signature V4
// request. headers are the signed headers, lowercase name to value;
// canonicalQuery is already canonical (sorted, encoded).
func signV4(method, canonicalURI, canonicalQuery string, headers map[string]string, payloadSHA256, accessKey, secretKey, region, service, amzDate string) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + strings.TrimSpace(headers[name]) + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	canonicalRequest := strings.Join([]string{
		method, canonicalURI, canonicalQuery, canonicalHeaders.String(), signedHeaders, payloadSHA256,
	}, "\n")
	date := amzDate[:8]
	scope := date + "/" + region + "/" + service + "/aws4_request"
	digest := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(digest[:])
	key := hmacSHA256([]byte("AWS4"+secretKey), date)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))
	return "AWS4-HMAC-SHA256 Credential=" + accessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// storeFromEnv builds the configured store: kind "s3" or "pvc".
func storeFromEnv(kind string, getenv func(string) string) (JobStore, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "s3", "minio":
		return newS3Store(getenv("FLEXINFER_JOBS_S3_ENDPOINT"), getenv("FLEXINFER_JOBS_S3_BUCKET"),
			getenv("FLEXINFER_JOBS_S3_REGION"), getenv("FLEXINFER_JOBS_S3_ACCESS_KEY"), getenv("FLEXINFER_JOBS_S3_SECRET_KEY"))
	case "pvc":
		return newPVCStore(getenv("FLEXINFER_JOBS_PVC_PATH"), getenv("FLEXINFER_JOBS_PVC_CLAIM"))
	case "":
		return nil, errors.New("FLEXINFER_JOBS_STORE is unset (s3 or pvc)")
	}
	return nil, fmt.Errorf("FLEXINFER_JOBS_STORE %q: want s3 or pvc", kind)
}

// parseByteSize parses a byte count with an optional Ki/Mi/Gi suffix.
func parseByteSize(raw string, fallback int64) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	mult := int64(1)
	for suffix, m := range map[string]int64{"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30} {
		if strings.HasSuffix(raw, suffix) {
			raw, mult = strings.TrimSuffix(raw, suffix), m
			break
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n * mult
}
