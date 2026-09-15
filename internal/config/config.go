// Package config reads acquire's runtime configuration from the environment.
// Blank optional values disable the corresponding feature so the service stays
// up (fail-open on config, fail-closed on auth).
package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr string // ADDR (default :8080)

	// Auth — the shared realm. AudienceRequired stays off (issuer-only).
	OIDCIssuer   string // OIDC_ISSUER
	OIDCAudience string // OIDC_AUDIENCE (default chino)
	OIDCClientID string // ACQUIRE_OIDC_CLIENT_ID — the SPA's public PKCE client (default laedeli-acquire)
	AdminRole    string // ACQUIRE_ADMIN_ROLE (default zaentrum-admin) — may grab/delete
	UserRole     string // ACQUIRE_USER_ROLE (default zaentrum-user)   — may request

	// Own database (acquire_beta on the shared cluster).
	DatabaseURL string // PG_URL / DATABASE_URL (pgx DSN)

	// Downstream services.
	GatewayURL  string // DOWNLOAD_GATEWAY_URL (http://download-gateway)
	KatalogURL  string // KATALOG_URL (katalog-api read, in-library check)
	ManagerURL  string // KATALOG_MANAGER_URL (katalog-manager write: create item + emit discovered)
	SvcTokenURL string // OIDC token endpoint for the service account (client-credentials)
	SvcClientID string // ACQUIRE_SVC_CLIENT_ID
	SvcSecret   string // ACQUIRE_SVC_CLIENT_SECRET

	// TMDB discovery (server-side key).
	TMDBAPIKey   string // TMDB_API_KEY
	TMDBLanguage string // TMDB_LANGUAGE (default en-US)

	// Search sources are configured in the console, not here. The protocol
	// preference only seeds the stored search settings on first boot.
	PreferUsenet bool // ACQUIRE_PREFER (default "usenet" -> NZB-first)

	// Kafka (shared cluster, mTLS). Prefix is the tenant namespace.
	KafkaBrokers     string // KAFKA_BROKERS
	KafkaCertDir     string // KAFKA_CERT_DIR (user.crt/user.key/ca.crt); set but empty = plaintext
	KafkaTopicPrefix string // KAFKA_TOPIC_PREFIX (default zaentrum-beta.)
	KafkaGroupID     string // KAFKA_GROUP_ID (default acquire)

	// Staging / packaging paths (on the shared media mount). Where a download
	// lands is now per client (its remote and local folder); DownloadsRoot is
	// the fallback for a client that declares no local folder.
	InboxRoot     string // ACQUIRE_INBOX_ROOT (default /var/lib/katalog/packages/_inbox)
	DownloadsRoot string // ACQUIRE_DOWNLOADS_ROOT (default /var/lib/katalog/packages/_downloads)

	// Endpoint policy for URLs an admin enters (internal/endpoint).
	EndpointDeny          []string // ACQUIRE_ENDPOINT_DENY: comma list of hostnames or domain suffixes
	EndpointAllowInternal bool     // ACQUIRE_ENDPOINT_ALLOW_INTERNAL: allow services in other namespaces
	PodNamespace          string   // POD_NAMESPACE, else the service account's namespace file
	ClusterDomain         string   // the cluster DNS domain from /etc/resolv.conf; "" outside a cluster
}

func env(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func def(d string, keys ...string) string {
	if v := env(keys...); v != "" {
		return v
	}
	return d
}

func Load() Config {
	downloads := def("/var/lib/katalog/packages/_downloads", "ACQUIRE_DOWNLOADS_ROOT")
	return Config{
		Addr:         def(":8080", "ADDR"),
		OIDCIssuer:   env("OIDC_ISSUER"),
		OIDCAudience: def("chino", "OIDC_AUDIENCE"),
		OIDCClientID: def("laedeli-acquire", "ACQUIRE_OIDC_CLIENT_ID"),
		AdminRole:    def("zaentrum-admin", "ACQUIRE_ADMIN_ROLE"),
		UserRole:     def("zaentrum-user", "ACQUIRE_USER_ROLE"),

		DatabaseURL: env("PG_URL", "DATABASE_URL"),

		GatewayURL:  env("DOWNLOAD_GATEWAY_URL"),
		KatalogURL:  env("KATALOG_URL"),
		ManagerURL:  env("KATALOG_MANAGER_URL"),
		SvcTokenURL: env("OIDC_TOKEN_URL"),
		SvcClientID: env("ACQUIRE_SVC_CLIENT_ID"),
		SvcSecret:   env("ACQUIRE_SVC_CLIENT_SECRET"),

		TMDBAPIKey:   env("TMDB_API_KEY"),
		TMDBLanguage: def("en-US", "TMDB_LANGUAGE"),

		PreferUsenet: def("usenet", "ACQUIRE_PREFER") == "usenet",

		KafkaBrokers:     env("KAFKA_BROKERS"),
		KafkaCertDir:     kafkaCertDir(),
		KafkaTopicPrefix: def("zaentrum-beta.", "KAFKA_TOPIC_PREFIX"),
		KafkaGroupID:     def("acquire", "KAFKA_GROUP_ID"),

		InboxRoot:     def("/var/lib/katalog/packages/_inbox", "ACQUIRE_INBOX_ROOT"),
		DownloadsRoot: downloads,

		EndpointDeny:          splitList(env("ACQUIRE_ENDPOINT_DENY")),
		EndpointAllowInternal: env("ACQUIRE_ENDPOINT_ALLOW_INTERNAL") == "true",
		PodNamespace:          podNamespace(),
		ClusterDomain:         clusterDomain(),
	}
}

// kafkaCertDir is where the brokers' client certificate lives. Unlike other
// settings, an empty value is not "unset": KAFKA_CERT_DIR set to "" means the
// brokers are a plaintext listener and no TLS is used. Unset keeps the
// /etc/kafka-cert mount.
func kafkaCertDir() string {
	v, set := os.LookupEnv("KAFKA_CERT_DIR")
	if !set {
		return "/etc/kafka-cert"
	}
	return strings.TrimSpace(v)
}

// serviceAccountNamespace is where Kubernetes mounts the pod's namespace.
const serviceAccountNamespace = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// podNamespace is acquire's own namespace: POD_NAMESPACE when the deployment
// sets it (downward API), else the mounted service account file, else "".
func podNamespace() string {
	if v := env("POD_NAMESPACE"); v != "" {
		return v
	}
	if b, err := os.ReadFile(serviceAccountNamespace); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

// clusterDomain is the cluster's DNS domain, read from the search list
// Kubernetes writes into a pod's resolv.conf (<ns>.svc.<domain>, svc.<domain>,
// <domain>); "" when the file has no such entry, i.e. outside a cluster.
func clusterDomain() string {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	return clusterDomainFrom(string(b))
}

func clusterDomainFrom(resolvConf string) string {
	for _, line := range strings.Split(resolvConf, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "search" {
			continue
		}
		for _, d := range f[1:] {
			d = strings.Trim(strings.ToLower(d), ".")
			if rest, ok := strings.CutPrefix(d, "svc."); ok && rest != "" {
				return rest
			}
		}
	}
	return ""
}

func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// retiredSearchEnv are the variables that pointed acquire at an external search
// aggregator. Search sources now live in acquire's own configuration.
var retiredSearchEnv = []string{"INDEXER_URL", "INDEXER_API_KEY", "PROWLARR_URL", "PROWLARR_API_KEY"}

// RetiredSearchEnv names the retired search variables that are still set, so
// the service can say once that they no longer do anything.
func RetiredSearchEnv() []string {
	var set []string
	for _, n := range retiredSearchEnv {
		if env(n) != "" {
			set = append(set, n)
		}
	}
	return set
}

// StorageFloorBytes is the free space below which acquire refuses to grab.
// Default 500 GB: the media export runs at 92% with 4.5 TB free and beta shares
// the SAME filesystem as production, so an unbounded sweep would eat prod's
// headroom. Override with ACQUIRE_STORAGE_FLOOR_GB.
func (c Config) StorageFloorBytes() int64 {
	gb := int64(500)
	if v := env("ACQUIRE_STORAGE_FLOOR_GB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			gb = n
		}
	}
	return gb << 30
}

// MaxConcurrentGrabs bounds in-flight downloads. There is no such cap anywhere
// today: a backlog sweep could start hundreds at once and fill the disk before
// the first one finishes, with nothing to stop it.
func (c Config) MaxConcurrentGrabs() int {
	n := 3
	if v := env("ACQUIRE_MAX_CONCURRENT_GRABS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	return n
}
