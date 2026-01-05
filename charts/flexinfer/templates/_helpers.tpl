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
{{- if .Values.serviceAccounts.controller.create -}}
{{- default (default (printf "%s-controller" (include "flexinfer.fullname" .)) .Values.serviceAccount.name) .Values.serviceAccounts.controller.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccounts.controller.name -}}
{{- end -}}
{{- end -}}

{{- define "flexinfer.agentServiceAccountName" -}}
{{- if .Values.serviceAccounts.agent.create -}}
{{- default (printf "%s-agent" (include "flexinfer.fullname" .)) .Values.serviceAccounts.agent.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccounts.agent.name -}}
{{- end -}}
{{- end -}}

{{- define "flexinfer.schedulerServiceAccountName" -}}
{{- if .Values.serviceAccounts.scheduler.create -}}
{{- default (printf "%s-scheduler" (include "flexinfer.fullname" .)) .Values.serviceAccounts.scheduler.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccounts.scheduler.name -}}
{{- end -}}
{{- end -}}

{{- define "flexinfer.proxyServiceAccountName" -}}
{{- if .Values.serviceAccounts.proxy.create -}}
{{- default (printf "%s-proxy" (include "flexinfer.fullname" .)) .Values.serviceAccounts.proxy.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccounts.proxy.name -}}
{{- end -}}
{{- end -}}

{{- define "flexinfer.benchmarkerServiceAccountName" -}}
{{- if .Values.serviceAccounts.benchmarker.create -}}
{{- default (printf "%s-benchmarker" (include "flexinfer.fullname" .)) .Values.serviceAccounts.benchmarker.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccounts.benchmarker.name -}}
{{- end -}}
{{- end -}}
