package termination

import (
	"os"
	"strings"
)

const (
	defaultMetadataBaseURL = "http://169.254.169.254"
	metadataBaseURLEnv     = "TERMINATION_METADATA_BASE_URL"
)

func metadataBaseURL() string {
	if v := strings.TrimSpace(os.Getenv(metadataBaseURLEnv)); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultMetadataBaseURL
}

func metadataURL(path string) string {
	return metadataBaseURL() + "/" + strings.TrimLeft(path, "/")
}

func awsProbeURL() string {
	return metadataURL("/latest/meta-data/")
}

func awsSpotActionURL() string {
	return metadataURL("/latest/meta-data/spot/instance-action")
}

func awsTokenURL() string {
	return metadataURL("/latest/api/token")
}

func gcpProbeURL() string {
	return metadataURL("/computeMetadata/v1/")
}

func gcpMaintenanceEventURL() string {
	return metadataURL("/computeMetadata/v1/instance/maintenance-event")
}

func gcpPreemptedURL() string {
	return metadataURL("/computeMetadata/v1/instance/preempted")
}

func azureProbeURL() string {
	return metadataURL("/metadata/instance?api-version=2021-02-01")
}

func azureScheduledEventsURL() string {
	return metadataURL("/metadata/scheduledevents?api-version=2020-07-01")
}
