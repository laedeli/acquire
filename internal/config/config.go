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

	// Indexer search backend (an aggregator you run). Blank -> auto-grab off.
	// INDEXER_URL / INDEXER_API_KEY are the current names; the PROWLARR_*
	// spellings are still read so an existing deployment keeps working.
	IndexerURL    string // INDEXER_URL (PROWLARR_URL), e.g. http://prowlarr:9696
	IndexerAPIKey string // INDEXER_API_KEY (PROWLARR_API_KEY)
	PreferUsenet  bool   // ACQUIRE_PREFER (default "usenet" -> NZB-first)

	// Kafka (shared cluster, mTLS). Prefix is the tenant namespace.
	KafkaBrokers     string // KAFKA_BROKERS
	KafkaCertDir     string // KAFKA_CERT_DIR (user.crt/user.key/ca.crt)
	KafkaTopicPrefix string // KAFKA_TOPIC_PREFIX (default zaentrum-beta.)
	KafkaGroupID     string // KAFKA_GROUP_ID (default acquire)

	// Staging / packaging paths (on the shared media mount).
	InboxRoot     string // ACQUIRE_INBOX_ROOT (default /var/lib/katalog/packages/_inbox)
	DownloadsRoot string // ACQUIRE_DOWNLOADS_ROOT (default /var/lib/katalog/packages/_downloads)
	SavePath      string // save path handed to the gateway (default = DownloadsRoot)
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

		IndexerURL:    firstEnv("INDEXER_URL", "PROWLARR_URL"),
		IndexerAPIKey: firstEnv("INDEXER_API_KEY", "PROWLARR_API_KEY"),
		PreferUsenet:  def("usenet", "ACQUIRE_PREFER") == "usenet",

		KafkaBrokers:     env("KAFKA_BROKERS"),
		KafkaCertDir:     def("/etc/kafka-cert", "KAFKA_CERT_DIR"),
		KafkaTopicPrefix: def("zaentrum-beta.", "KAFKA_TOPIC_PREFIX"),
		KafkaGroupID:     def("acquire", "KAFKA_GROUP_ID"),

		InboxRoot:     def("/var/lib/katalog/packages/_inbox", "ACQUIRE_INBOX_ROOT"),
		DownloadsRoot: downloads,
		SavePath:      def(downloads, "ACQUIRE_SAVE_PATH"),
	}
}

// firstEnv returns the first of names that is set, so a renamed variable can be
// introduced without breaking a deployment still setting the old one.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := env(n); v != "" {
			return v
		}
	}
	return ""
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
