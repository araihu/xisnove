{{- define "xisnove-edge.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "xisnove-edge.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "xisnove-edge.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "xisnove-edge.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | quote }}
app.kubernetes.io/name: {{ include "xisnove-edge.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "xisnove-edge.operatorServiceAccount" -}}
{{- printf "%s-operator" (include "xisnove-edge.fullname" .) -}}
{{- end -}}

{{- define "xisnove-edge.discoveryServiceAccount" -}}
{{- printf "%s-discovery" (include "xisnove-edge.fullname" .) -}}
{{- end -}}

{{- define "xisnove-edge.agentName" -}}
{{- default (include "xisnove-edge.fullname" .) .Values.agent.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "xisnove-edge.agentCredentialSecret" -}}
{{- default (printf "%s-credential" (include "xisnove-edge.agentName" .)) .Values.agent.credentialSecret.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "xisnove-edge.operatorImage" -}}
{{- printf "%s:%s" .Values.operator.image.repository (default .Chart.AppVersion .Values.operator.image.tag) -}}
{{- end -}}

{{- define "xisnove-edge.agentImage" -}}
{{- printf "%s:%s" .Values.agent.image.repository (default .Chart.AppVersion .Values.agent.image.tag) -}}
{{- end -}}
