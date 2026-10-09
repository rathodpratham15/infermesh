{{/* Release-scoped base name. */}}
{{- define "infermesh.fullname" -}}
{{- if contains "infermesh" .Release.Name -}}
{{- .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-infermesh" .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "infermesh.labels" -}}
app.kubernetes.io/name: infermesh
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end -}}

{{/* Selector labels for one component. Usage: include "infermesh.selector" (dict "ctx" . "component" "gateway") */}}
{{- define "infermesh.selector" -}}
app.kubernetes.io/name: infermesh
app.kubernetes.io/instance: {{ .ctx.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* Image reference. Usage: include "infermesh.image" (dict "image" .Values.gateway.image "ctx" .) */}}
{{- define "infermesh.image" -}}
{{- printf "%s:%s" .image.repository (.image.tag | default .ctx.Chart.AppVersion) -}}
{{- end -}}

{{/* Kafka bootstrap servers: the in-chart broker, an external one, or empty (events disabled). */}}
{{- define "infermesh.kafkaBrokers" -}}
{{- if .Values.kafka.enabled -}}
{{- printf "%s-kafka:9092" (include "infermesh.fullname" .) -}}
{{- else -}}
{{- .Values.kafka.externalBrokers -}}
{{- end -}}
{{- end -}}

{{/* Env var prefix for a backend name: "my-engine" -> "BACKEND_MY_ENGINE_". */}}
{{- define "infermesh.backendPrefix" -}}
{{- printf "BACKEND_%s_" (upper (replace "-" "_" .)) -}}
{{- end -}}

{{- define "infermesh.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}{{ include "infermesh.fullname" . }}{{- else -}}{{ .Values.serviceAccount.name | default "default" }}{{- end -}}
{{- end -}}

{{/* Restricted container security context shared by the Go services. */}}
{{- define "infermesh.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
runAsNonRoot: true
capabilities:
  drop: ["ALL"]
{{- end -}}
