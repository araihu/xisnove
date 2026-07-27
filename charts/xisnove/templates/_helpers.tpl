{{- define "xisnove.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "xisnove.fullname" -}}
{{- if .Values.fullnameOverride }}{{ .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}{{ else }}{{ printf "%s-%s" .Release.Name (include "xisnove.name" .) | trunc 63 | trimSuffix "-" }}{{ end -}}
{{- end -}}
{{- define "xisnove.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | quote }}
app.kubernetes.io/name: {{ include "xisnove.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
{{- define "xisnove.selectorLabels" -}}
app.kubernetes.io/name: {{ include "xisnove.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
{{- define "xisnove.serverImage" -}}{{ printf "%s:%s" .Values.server.image.repository (default .Chart.AppVersion .Values.server.image.tag) }}{{- end -}}
{{- define "xisnove.uiImage" -}}{{ printf "%s:%s" .Values.ui.image.repository (default .Chart.AppVersion .Values.ui.image.tag) }}{{- end -}}
{{- define "xisnove.agentImage" -}}{{ printf "%s:%s" .Values.agent.image.repository (default .Chart.AppVersion .Values.agent.image.tag) }}{{- end -}}
{{- define "xisnove.serverServiceAccount" -}}{{ default (printf "%s-server" (include "xisnove.fullname" .)) .Values.server.serviceAccount.name }}{{- end -}}
{{- define "xisnove.uiServiceAccount" -}}{{ default (printf "%s-ui" (include "xisnove.fullname" .)) .Values.ui.serviceAccount.name }}{{- end -}}
{{- define "xisnove.agentServiceAccount" -}}{{ default (printf "%s-agent" (include "xisnove.fullname" .)) .Values.agent.serviceAccount.name }}{{- end -}}
{{- define "xisnove.databaseProfile" -}}{{ if eq .Values.database.profile "tursoManaged" }}turso-cloud{{ else }}{{ .Values.database.profile }}{{ end }}{{- end -}}
{{- define "xisnove.databaseURLPath" -}}{{ if eq .Values.database.profile "sqlite" }}/var/lib/xisnove/xisnove.db{{ else }}/var/run/secrets/xisnove/database/url{{ end }}{{- end -}}
{{- define "xisnove.databaseArgs" -}}
- --database-profile
- {{ include "xisnove.databaseProfile" . | quote }}
{{- if eq .Values.database.profile "sqlite" }}
- --database-url
{{- else }}
- --database-url-file
{{- end }}
- {{ include "xisnove.databaseURLPath" . | quote }}
{{- if eq .Values.database.profile "tursoManaged" }}
- --database-auth-token-file
- /var/run/secrets/xisnove/database/auth-token
{{- end }}
- --installation-id
- {{ .Values.database.installationID | quote }}
{{- end -}}
{{- define "xisnove.serverSecretVolume" -}}
- name: server-secret
  secret:
    secretName: {{ required "secrets.server.existingSecret.name is required" .Values.secrets.server.existingSecret.name }}
    defaultMode: 0440
    items:
      - key: {{ .Values.secrets.server.existingSecret.cursorSigningKeyKey }}
        path: cursor-signing-key
      - key: {{ .Values.secrets.server.existingSecret.notificationMasterKeyKey }}
        path: notification-master-key
{{- end -}}
{{- define "xisnove.databaseSecretVolume" -}}
{{- if eq .Values.database.profile "sqlite" }}
- name: database
  persistentVolumeClaim:
    claimName: data
{{- else }}
- name: database-secret
  secret:
    secretName: {{ if eq .Values.database.profile "postgres" }}{{ required "database.postgres.existingSecret.name is required" .Values.database.postgres.existingSecret.name }}{{ else }}{{ required "database.tursoManaged.existingSecret.name is required" .Values.database.tursoManaged.existingSecret.name }}{{ end }}
    defaultMode: 0440
    items:
      - key: {{ if eq .Values.database.profile "postgres" }}{{ .Values.database.postgres.existingSecret.urlKey }}{{ else }}{{ .Values.database.tursoManaged.existingSecret.urlKey }}{{ end }}
        path: url
      {{- if eq .Values.database.profile "tursoManaged" }}
      - key: {{ .Values.database.tursoManaged.existingSecret.authTokenKey }}
        path: auth-token
      {{- end }}
{{- end -}}
{{- end -}}
{{- define "xisnove.databaseVolumeMount" -}}
{{- if eq .Values.database.profile "sqlite" }}
- name: data
  mountPath: /var/lib/xisnove
{{- else }}
- name: database-secret
  mountPath: /var/run/secrets/xisnove/database
  readOnly: true
{{- end -}}
{{- end -}}
