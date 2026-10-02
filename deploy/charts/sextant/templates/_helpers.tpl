{{- define "sextant.name" -}}{{ .Chart.Name }}{{- end -}}

{{- define "sextant.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}{{ .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else -}}{{ printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}{{- end -}}
{{- end -}}

{{- define "sextant.labels" -}}
app.kubernetes.io/name: {{ include "sextant.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "sextant.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sextant.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "sextant.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}

{{/* Fail early, with a useful message, instead of rendering a broken release. */}}
{{- define "sextant.validate" -}}
{{- $_ := required "database.existingSecret is required (Secret with app-dsn and owner-dsn)" .Values.database.existingSecret -}}
{{- $_ := required "tls.existingSecret is required (kubernetes.io/tls Secret)" .Values.tls.existingSecret -}}
{{- $_ := required "pki.existingSecret is required (Secret with root.crt and root.key)" .Values.pki.existingSecret -}}
{{- if .Values.dev.enabled -}}
{{- if not .Values.dev.iUnderstandThisIsInsecure -}}{{- fail "dev.enabled grants a static token full access to a tenant; set dev.iUnderstandThisIsInsecure=true to confirm" -}}{{- end -}}
{{- $_ := required "dev.tenant is required with dev.enabled" .Values.dev.tenant -}}
{{- $_ := required "dev.tokenSecret is required with dev.enabled" .Values.dev.tokenSecret -}}
{{- end -}}
{{- if and (eq .Values.service.type "ClusterIP") .Values.service.nodePort -}}{{- fail "service.nodePort is only valid with service.type NodePort" -}}{{- end -}}
{{- end -}}
