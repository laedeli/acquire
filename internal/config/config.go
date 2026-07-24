// Package config reads acquire's runtime configuration from the environment.
// Blank optional values disable the corresponding feature so the service stays
// up (fail-open on config, fail-closed on auth).
package config

import (
	"os"
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

		KafkaBrokers:     env("KAFKA_BROKERS"),
		KafkaCertDir:     def("/etc/kafka-cert", "KAFKA_CERT_DIR"),
		KafkaTopicPrefix: def("zaentrum-beta.", "KAFKA_TOPIC_PREFIX"),
		KafkaGroupID:     def("acquire", "KAFKA_GROUP_ID"),

		InboxRoot:     def("/var/lib/katalog/packages/_inbox", "ACQUIRE_INBOX_ROOT"),
		DownloadsRoot: downloads,
		SavePath:      def(downloads, "ACQUIRE_SAVE_PATH"),
	}
}
