{{- define "sextant-agent.name" -}}{{ .Chart.Name }}{{- end -}}

{{- define "sextant-agent.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}{{ .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else -}}{{ printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}{{- end -}}
{{- end -}}

{{- define "sextant-agent.labels" -}}
app.kubernetes.io/name: {{ include "sextant-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "sextant-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sextant-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "sextant-agent.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}

{{- define "sextant-agent.credentialsSecret" -}}{{ include "sextant-agent.fullname" . }}-credentials{{- end -}}

{{- define "sextant-agent.tokenSecret" -}}
{{- if .Values.registration.existingSecret -}}{{ .Values.registration.existingSecret }}{{- else -}}{{ include "sextant-agent.fullname" . }}-registration{{- end -}}
{{- end -}}

{{- define "sextant-agent.validate" -}}
{{- $url := required "managementURL is required (https://host[:port])" .Values.managementURL -}}
{{- if not (hasPrefix "https://" $url) -}}{{- fail "managementURL must start with https://" -}}{{- end -}}
{{- end -}}
