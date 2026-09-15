{{/*
Conventions shared by the acquire addon's templates. Platform facts are read
from .Values.zaentrum only: the operator sets that block, and it overrides
anything an install or the chart gives.
*/}}

{{/* acquire.secretName — the Secret the secret inputs are rendered into. */}}
{{- define "acquire.secretName" -}}acquire-secrets{{- end -}}

{{/* acquire.kafkaCertDir — where the event brokers' client certificate is mounted. */}}
{{- define "acquire.kafkaCertDir" -}}/etc/kafka-cert{{- end -}}

{{/*
acquire.labels — metadata labels of one component. Selectors stay `app` alone:
a Deployment's selector is immutable, so the grouping labels live in metadata.
Use: {{- include "acquire.labels" (dict "root" $ "component" "acquire") | nindent 4 }}
*/}}
{{- define "acquire.labels" -}}
app: {{ .component }}
zaentrum.io/addon: {{ .root.Release.Name | quote }}
zaentrum.io/component: {{ .component }}
{{- with .root.Values.zaentrum.partOf }}
app.kubernetes.io/part-of: {{ . | quote }}
{{- end }}
{{- end -}}

{{/* acquire.issuer — the platform's OIDC issuer, exactly as tokens carry it. */}}
{{- define "acquire.issuer" -}}
{{- required "zaentrum.issuer is required: the platform operator sets it" .Values.zaentrum.issuer -}}
{{- end -}}

{{/* acquire.tokenURL — the service account's token endpoint, derived from the
     issuer the way the platform derives its own workers' one. */}}
{{- define "acquire.tokenURL" -}}
{{- include "acquire.issuer" . | trimSuffix "/" -}}/protocol/openid-connect/token
{{- end -}}

{{/* acquire.topicPrefix — the tenant's topic namespace (the platform default when unset). */}}
{{- define "acquire.topicPrefix" -}}
{{- .Values.zaentrum.events.topicPrefix | default "stube." -}}
{{- end -}}

{{/* acquire.kafkaGroupID — acquire's consumer group: the topic prefix without
     its trailing dot, then "-acquire" (zaentrum-example. → zaentrum-example-acquire). */}}
{{- define "acquire.kafkaGroupID" -}}
{{- $prefix := include "acquire.topicPrefix" . | trimSuffix "." -}}
{{- if $prefix -}}{{ $prefix }}-acquire{{- else -}}acquire{{- end -}}
{{- end -}}

{{/*
acquire.hostAliases — resolve the issuer's host to zaentrum.issuerHostAliasIP
(split horizon), so token validation inside the cluster reaches the issuer.
Emits nothing when the address is unset. Place under a pod spec.
*/}}
{{- define "acquire.hostAliases" -}}
{{- with .Values.zaentrum.issuerHostAliasIP -}}
hostAliases:
  - ip: {{ . | quote }}
    hostnames:
      - {{ (urlParse (include "acquire.issuer" $)).hostname | quote }}
{{- end -}}
{{- end -}}

{{/* acquire.imagePullSecrets — the platform's pull secrets, or nothing. */}}
{{- define "acquire.imagePullSecrets" -}}
{{- with .Values.zaentrum.imagePullSecrets -}}
imagePullSecrets:
{{- range . }}
  - name: {{ if kindIs "map" . }}{{ .name | quote }}{{ else }}{{ . | quote }}{{ end }}
{{- end }}
{{- end -}}
{{- end -}}

{{/* acquire.image — repository:tag; an empty tag is the chart's appVersion.
     Use: {{ include "acquire.image" (dict "root" $ "image" .Values.acquire.image) }} */}}
{{- define "acquire.image" -}}
{{ .image.repository }}:{{ .image.tag | default .root.Chart.AppVersion }}
{{- end -}}

{{/*
acquire.podSecurityContext / acquire.containerSecurityContext — what the
operator's guardrails require, set explicitly rather than left to its defaults.
No runAsUser: both images run as their distroless non-root user, and OpenShift
assigns one from the namespace's range.
*/}}
{{- define "acquire.podSecurityContext" -}}
runAsNonRoot: true
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "acquire.containerSecurityContext" -}}
allowPrivilegeEscalation: false
capabilities:
  drop: [ALL]
{{- end -}}
