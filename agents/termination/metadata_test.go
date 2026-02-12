package termination

import "testing"

func TestMetadataBaseURLDefault(t *testing.T) {
	t.Setenv(metadataBaseURLEnv, "")
	if got := metadataBaseURL(); got != defaultMetadataBaseURL {
		t.Fatalf("metadataBaseURL() = %q, want %q", got, defaultMetadataBaseURL)
	}
}

func TestMetadataBaseURLOverride(t *testing.T) {
	t.Setenv(metadataBaseURLEnv, "http://127.0.0.1:18080/")
	if got := metadataBaseURL(); got != "http://127.0.0.1:18080" {
		t.Fatalf("metadataBaseURL() = %q, want %q", got, "http://127.0.0.1:18080")
	}
	if got := awsSpotActionURL(); got != "http://127.0.0.1:18080/latest/meta-data/spot/instance-action" {
		t.Fatalf("awsSpotActionURL() = %q", got)
	}
}
