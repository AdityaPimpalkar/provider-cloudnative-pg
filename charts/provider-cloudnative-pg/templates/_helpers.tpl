{{/*
Expand the name of the chart.
*/}}
{{- define "provider-cloudnative-pg.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "provider-cloudnative-pg.fullname" -}}
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
{{- define "provider-cloudnative-pg.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "provider-cloudnative-pg.labels" -}}
helm.sh/chart: {{ include "provider-cloudnative-pg.chart" . }}
{{ include "provider-cloudnative-pg.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "provider-cloudnative-pg.selectorLabels" -}}
app.kubernetes.io/name: {{ include "provider-cloudnative-pg.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "provider-cloudnative-pg.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "provider-cloudnative-pg.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Container image
*/}}
{{- define "provider-cloudnative-pg.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Name of the Secret holding the admission webhook's serving certificate
*/}}
{{- define "provider-cloudnative-pg.admissionSecretName" -}}
{{- printf "%s-admission-cert" (include "provider-cloudnative-pg.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
The admission webhook's serving certificate: from values, else the Secret the
previous release created, else a new CA and certificate.
*/}}
{{- define "provider-cloudnative-pg.admissionCerts" -}}
{{- $certs := .Values.admission.certs }}
{{- $current := lookup "v1" "Secret" .Release.Namespace (include "provider-cloudnative-pg.admissionSecretName" .) }}
{{- if and (index $certs "tls.crt") (index $certs "tls.key") (index $certs "ca.crt") }}
tls.crt: {{ index $certs "tls.crt" | b64enc }}
tls.key: {{ index $certs "tls.key" | b64enc }}
ca.crt: {{ index $certs "ca.crt" | b64enc }}
{{- else if and $current (index $current.data "ca.crt") }}
tls.crt: {{ index $current.data "tls.crt" }}
tls.key: {{ index $current.data "tls.key" }}
ca.crt: {{ index $current.data "ca.crt" }}
{{- else }}
{{- $service := include "provider-cloudnative-pg.fullname" . }}
{{- $dnsName := printf "%s.%s.svc" $service .Release.Namespace }}
{{- $altNames := list $service (printf "%s.%s" $service .Release.Namespace) $dnsName (printf "%s.cluster.local" $dnsName) }}
{{- $ca := genCA (printf "%s-admission-ca" $service) 3650 }}
{{- $cert := genSignedCert $dnsName nil $altNames 3650 $ca }}
tls.crt: {{ $cert.Cert | b64enc }}
tls.key: {{ $cert.Key | b64enc }}
ca.crt: {{ $ca.Cert | b64enc }}
{{- end }}
{{- end }}
