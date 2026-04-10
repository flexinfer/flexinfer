{{/*
Expand the name of the chart.
*/}}
{{- define "flexinfer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "flexinfer.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "flexinfer.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "flexinfer.labels" -}}
helm.sh/chart: {{ include "flexinfer.chart" . }}
{{ include "flexinfer.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "flexinfer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "flexinfer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "flexinfer.serviceAccountName" -}}
{{- include "flexinfer.controllerServiceAccountName" . }}
{{- end }}

{{- define "flexinfer.controllerServiceAccountName" -}}
{{- $serviceAccounts := default (dict) .Values.serviceAccounts -}}
{{- $controller := default (dict) (get $serviceAccounts "controller") -}}
{{- $legacy := default (dict) .Values.serviceAccount -}}
{{- if default true (get $controller "create") -}}
{{- default (default (printf "%s-controller" (include "flexinfer.fullname" .)) (get $legacy "name")) (get $controller "name") -}}
{{- else -}}
{{- default "default" (get $controller "name") -}}
{{- end -}}
{{- end -}}

{{- define "flexinfer.agentServiceAccountName" -}}
{{- $serviceAccounts := default (dict) .Values.serviceAccounts -}}
{{- $agent := default (dict) (get $serviceAccounts "agent") -}}
{{- if default true (get $agent "create") -}}
{{- default (printf "%s-agent" (include "flexinfer.fullname" .)) (get $agent "name") -}}
{{- else -}}
{{- default "default" (get $agent "name") -}}
{{- end -}}
{{- end -}}

{{- define "flexinfer.schedulerServiceAccountName" -}}
{{- $serviceAccounts := default (dict) .Values.serviceAccounts -}}
{{- $scheduler := default (dict) (get $serviceAccounts "scheduler") -}}
{{- if default true (get $scheduler "create") -}}
{{- default (printf "%s-scheduler" (include "flexinfer.fullname" .)) (get $scheduler "name") -}}
{{- else -}}
{{- default "default" (get $scheduler "name") -}}
{{- end -}}
{{- end -}}

{{- define "flexinfer.proxyServiceAccountName" -}}
{{- $serviceAccounts := default (dict) .Values.serviceAccounts -}}
{{- $proxy := default (dict) (get $serviceAccounts "proxy") -}}
{{- if default true (get $proxy "create") -}}
{{- default (printf "%s-proxy" (include "flexinfer.fullname" .)) (get $proxy "name") -}}
{{- else -}}
{{- default "default" (get $proxy "name") -}}
{{- end -}}
{{- end -}}

{{/*
Resolve an image reference for a given image config block.
If digest is set, prefer repository@digest. Otherwise use repository:tag.
Usage: {{ include "flexinfer.imageRef" .Values.controller.image }}
*/}}
{{- define "flexinfer.imageRef" -}}
{{- $repository := required "image.repository is required" .repository -}}
{{- if .digest -}}
  {{- printf "%s@%s" $repository .digest -}}
{{- else -}}
  {{- printf "%s:%s" $repository (.tag | default "latest") -}}
{{- end -}}
{{- end -}}

{{/*
Resolve imagePullPolicy for a given image config block.
If pullPolicy is explicitly set, use it. Otherwise auto-detect:
  - "Always" for mutable tags (master, latest, empty)
  - "IfNotPresent" for immutable tags (anything else)
Usage: {{ include "flexinfer.imagePullPolicy" .Values.controller.image }}
*/}}
{{- define "flexinfer.imagePullPolicy" -}}
{{- if .pullPolicy -}}
  {{- .pullPolicy -}}
{{- else -}}
  {{- if .digest -}}
    IfNotPresent
  {{- else -}}
  {{- $tag := .tag | default "" -}}
  {{- if or (eq $tag "") (eq $tag "latest") (eq $tag "master") (eq $tag "main") -}}
    Always
  {{- else -}}
    IfNotPresent
  {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{- define "flexinfer.benchmarkerServiceAccountName" -}}
{{- $serviceAccounts := default (dict) .Values.serviceAccounts -}}
{{- $benchmarker := default (dict) (get $serviceAccounts "benchmarker") -}}
{{- if default true (get $benchmarker "create") -}}
{{- default (printf "%s-benchmarker" (include "flexinfer.fullname" .)) (get $benchmarker "name") -}}
{{- else -}}
{{- default "default" (get $benchmarker "name") -}}
{{- end -}}
{{- end -}}
