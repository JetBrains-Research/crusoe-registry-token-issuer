{{/*
Expand the name of the chart.
*/}}
{{- define "crusoe-registry-token-issuer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "crusoe-registry-token-issuer.fullname" -}}
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
{{- define "crusoe-registry-token-issuer.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "crusoe-registry-token-issuer.labels" -}}
helm.sh/chart: {{ include "crusoe-registry-token-issuer.chart" . }}
{{ include "crusoe-registry-token-issuer.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "crusoe-registry-token-issuer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "crusoe-registry-token-issuer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Issuer API base path.
*/}}
{{- define "crusoe-registry-token-issuer.basePath" -}}
{{ .Values.config.server.basePath | default "/api/v1" }}
{{- end }}

{{/*
Deployment image.
*/}}
{{- define "crusoe-registry-token-issuer.image" -}}
{{ .Values.deployment.image.repository }}:{{ .Values.deployment.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "crusoe-registry-token-issuer.serviceAccountName" -}}
{{- $default := (include "crusoe-registry-token-issuer.fullname" .) }}
{{- with .Values.serviceAccount }}
{{- if .create }}
{{- default $default .name }}
{{- else }}
{{- default "default" .name }}
{{- end }}
{{- end }}
{{- end }}
