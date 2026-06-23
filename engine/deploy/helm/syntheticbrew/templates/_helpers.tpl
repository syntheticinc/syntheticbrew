{{/*
Expand the name of the chart.
*/}}
{{- define "syntheticbrew-engine.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "syntheticbrew-engine.fullname" -}}
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
{{- define "syntheticbrew-engine.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "syntheticbrew-engine.labels" -}}
helm.sh/chart: {{ include "syntheticbrew-engine.chart" . }}
{{ include "syntheticbrew-engine.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "syntheticbrew-engine.selectorLabels" -}}
app.kubernetes.io/name: {{ include "syntheticbrew-engine.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Build the DATABASE_URL from postgresql values.
Only used when postgresql.external.existingSecret is not set.
*/}}
{{- define "syntheticbrew-engine.databaseURL" -}}
{{- with .Values.postgresql.external -}}
postgres://{{ .username }}:{{ .password }}@{{ .host }}:{{ .port }}/{{ .database }}?sslmode={{ .sslmode }}
{{- end }}
{{- end }}

{{/*
Return the name of the Secret that holds DATABASE_URL.
Uses existingSecret when provided, otherwise the chart-managed Secret.
*/}}
{{- define "syntheticbrew-engine.databaseSecretName" -}}
{{- if .Values.postgresql.external.existingSecret -}}
{{- .Values.postgresql.external.existingSecret -}}
{{- else -}}
{{- printf "%s-secrets" (include "syntheticbrew-engine.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Return the Secret key that holds DATABASE_URL.
*/}}
{{- define "syntheticbrew-engine.databaseSecretKey" -}}
{{- if .Values.postgresql.external.existingSecret -}}
{{- .Values.postgresql.external.existingSecretKey | default "DATABASE_URL" -}}
{{- else -}}
database-url
{{- end -}}
{{- end }}

{{/*
Return the derived knowledge raw-file storage mode: "none" or "local".
When config.knowledge.storage is set, use it verbatim. Otherwise derive:
"local" if persistence.knowledge.enabled, else "none" (back-compat).
Single-sourced so the deployment env, volume gating, and PVC agree.
*/}}
{{- define "syntheticbrew-engine.knowledgeStorage" -}}
{{- if .Values.config.knowledge.storage -}}
{{- .Values.config.knowledge.storage -}}
{{- else if .Values.persistence.knowledge.enabled -}}
local
{{- else -}}
none
{{- end -}}
{{- end }}

{{/*
Return the ServiceAccount name.
*/}}
{{- define "syntheticbrew-engine.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "syntheticbrew-engine.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end }}
