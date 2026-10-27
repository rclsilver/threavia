{{- define "executor.name" -}}
{{- default (printf "%s-executor" .Release.Name) .Values.fullnameOverride | trunc 50 | trimSuffix "-" -}}
{{- end -}}
{{- define "executor.ca" -}}
{{- default (printf "%s-ca" (include "executor.name" .)) .Values.tls.caSecret -}}
{{- end -}}
{{- define "executor.tls" -}}
{{- default (printf "%s-tls" (include "executor.name" .)) .Values.tls.existingSecret -}}
{{- end -}}
{{- define "executor.url" -}}
{{- default (printf "https://%s.%s.svc:8443" (include "executor.name" .) .Release.Namespace) .Values.publicURL -}}
{{- end -}}
