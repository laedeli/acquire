{{/*
Conventions shared by the acquire addon's templates. Platform facts are read
from .Values.zaentrum only: the operator sets that block, and it overrides
anything an install or the chart gives.
*/}}

{{/* acquire.secretName — the Secret the secret inputs are rendered into. */}}
{{- define "acquire.secretName" -}}acquire-secrets{{- end -}}

{{/* acquire.kafkaCertDir — where the event brokers' client certificate is mounted. */}}
{{- define "acquire.kafkaCertDir" -}}/etc/kafka-cert{{- end -}}

{{/* acquire.mediaMount — where acquire mounts the platform's media claim (read-only). */}}
{{- define "acquire.mediaMount" -}}/var/lib/katalog{{- end -}}

{{/* acquire.arrivals — the library's arrivals folder on the media claim, the
     only place the catalog's ingest takes a file from. acquire copies the
     finished downloads into it, the one part of the claim it writes. */}}
{{- define "acquire.arrivals" -}}.work/incoming{{- end -}}

{{/* acquire.downloadsClaim — the claim the download clients work on (the
     release is always acquire). */}}
{{- define "acquire.downloadsClaim" -}}acquire-downloads{{- end -}}

{{/* acquire.downloadsMount — where acquire mounts it, read-only. */}}
{{- define "acquire.downloadsMount" -}}/var/lib/acquire/downloads{{- end -}}

{{/* acquire.anyClient — "true" when the chart runs a download client. */}}
{{- define "acquire.anyClient" -}}
{{- if or .Values.clients.qbittorrent.enabled .Values.clients.nzbget.enabled }}true{{ end -}}
{{- end -}}

{{/*
acquire.bytes — a storage quantity (a whole number, maybe with Ki, Mi, Gi, Ti
or k, M, G, T) as a byte count. Use: {{ include "acquire.bytes" "300Gi" }}
*/}}
{{- define "acquire.bytes" -}}
{{- $q := toString . | trim -}}
{{- $n := regexFind "^[0-9]+" $q -}}
{{- $unit := trimPrefix $n $q -}}
{{- $units := dict "" 1 "Ki" 1024 "Mi" 1048576 "Gi" 1073741824 "Ti" 1099511627776 "k" 1000 "M" 1000000 "G" 1000000000 "T" 1000000000000 -}}
{{- if or (not $n) (not (hasKey $units $unit)) -}}
{{- fail (printf "%q is no storage size this chart reads: a whole number, maybe with Ki, Mi, Gi, Ti, k, M, G or T" $q) -}}
{{- end -}}
{{- mul (atoi $n) (index $units $unit) -}}
{{- end -}}

{{/* acquire.downloadsReserve — the bytes kept free on the downloads volume:
     clients.downloads.reserve, or a tenth of its size. NZBGet pauses below
     it, and acquire hands over no release that would eat into it. */}}
{{- define "acquire.downloadsReserve" -}}
{{- $d := .Values.clients.downloads -}}
{{- if $d.reserve -}}{{ include "acquire.bytes" $d.reserve }}{{- else -}}{{ div (include "acquire.bytes" $d.size | atoi) 10 }}{{- end -}}
{{- end -}}

{{/*
acquire.clients — the download clients the chart can run, and what is fixed
about each: its gateway type, the protocols it takes and the port of its Web UI
and API. Their values live under .Values.clients.<name>.
Use: {{- $kinds := include "acquire.clients" . | fromYaml }}
*/}}
{{- define "acquire.clients" -}}
qbittorrent:
  type: qbittorrent
  protocols: [torrent]
  port: 8080
nzbget:
  type: nzbget
  protocols: [usenet]
  port: 6789
{{- end -}}

{{/* acquire.clientNames — the clients in a fixed order. Use: {{- range $name := include "acquire.clientNames" . | fromJsonArray }} */}}
{{- define "acquire.clientNames" -}}["qbittorrent","nzbget"]{{- end -}}

{{/* acquire.clientPassword — a client's password, which the operator generates
     when none is given. Use: {{ include "acquire.clientPassword" (dict "root" $ "name" "nzbget") }} */}}
{{- define "acquire.clientPassword" -}}
{{- required (printf "clients.%s.password is required (the operator generates one when none is given)" .name) (index .root.Values.clients .name).password -}}
{{- end -}}

{{/* acquire.vpnProxy — the platform VPN's SOCKS5 proxy (zaentrum.vpn.proxy,
     socks5h://host:port), or "" when the platform has none. */}}
{{- define "acquire.vpnProxy" -}}
{{- (.Values.zaentrum.vpn | default dict).proxy | default "" -}}
{{- end -}}

{{/*
acquire.managedClients — ACQUIRE_MANAGED_CLIENTS: the clients the chart runs,
as acquire stores them (internal/app/managed.go). It carries their passwords,
so it is rendered into the chart's Secret only. remotePath is the folder of
finished downloads as the client sees it, localPath the same folder under
acquire's read-only mount of the downloads claim.
Always a list: an empty one makes acquire remove the clients it stored for an
earlier render.
*/}}
{{- define "acquire.managedClients" -}}
{{- $kinds := include "acquire.clients" . | fromYaml -}}
{{- $out := list -}}
{{- range $name := include "acquire.clientNames" . | fromJsonArray -}}
{{- $c := index $.Values.clients $name -}}
{{- if $c.enabled -}}
{{- $k := index $kinds $name -}}
{{- $out = append $out (dict
      "id" $name
      "type" $k.type
      "baseUrl" (printf "http://%s:%d" $name (int $k.port))
      "auth" "basic"
      "username" (required (printf "clients.%s.username is required" $name) $c.username)
      "secret" (include "acquire.clientPassword" (dict "root" $ "name" $name))
      "protocols" $k.protocols
      "category" "acquire"
      "remotePath" "/downloads/complete"
      "localPath" (printf "%s/%s/complete" (include "acquire.downloadsMount" $) $name)) -}}
{{- end -}}
{{- end -}}
{{- toJson $out -}}
{{- end -}}

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

{{/* acquire.platformURL — ACQUIRE_PLATFORM_URL: the platform's address, scheme
     and host, from zaentrum.hostname. Its portal hosts the console, at
     /portal/app/acquire. https, unless the issuer is plain http on that same
     host (a platform without TLS); "" without a hostname. */}}
{{- define "acquire.platformURL" -}}
{{- with .Values.zaentrum.hostname -}}
{{- $issuer := urlParse (include "acquire.issuer" $) -}}
{{- if and (eq $issuer.scheme "http") (eq $issuer.host .) -}}http{{- else -}}https{{- end -}}://{{ . }}
{{- end -}}
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

{{/* acquire.downloadsPodSecurityContext — a pod on the downloads claim: where
     the platform gives pods a group for their volumes, its ownership is set
     only when the volume's root differs, not file by file on every start. */}}
{{- define "acquire.downloadsPodSecurityContext" -}}
{{ include "acquire.podSecurityContext" . }}
fsGroupChangePolicy: OnRootMismatch
{{- end -}}

{{- define "acquire.containerSecurityContext" -}}
allowPrivilegeEscalation: false
capabilities:
  drop: [ALL]
{{- end -}}
