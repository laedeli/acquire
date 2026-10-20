// Package config reads acquire's runtime configuration from the environment.
// Blank optional values disable the corresponding feature so the service stays
// up (fail-open on config, fail-closed on auth).
package config

import (
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr string // ADDR (default :8080)

	// Auth — the shared realm. AudienceRequired stays off (issuer-only).
	OIDCIssuer   string // OIDC_ISSUER
	OIDCAudience string // OIDC_AUDIENCE (default chino)
	AdminRole    string // ACQUIRE_ADMIN_ROLE (default zaentrum-admin) — may grab/delete
	UserRole     string // ACQUIRE_USER_ROLE (default zaentrum-user)   — may request

	// PlatformURL (ACQUIRE_PLATFORM_URL) is the platform's public address,
	// scheme and host only (https://zaentrum.example.org); a bare host means
	// https. Its portal hosts the console, and a browser that asks acquire for
	// a page is sent to acquire's app there. "" sends it to the portal's root
	// on the host it asked.
	PlatformURL string

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

	// Copy-on-import. ArrivalsRoot (ACQUIRE_ARRIVALS_ROOT) is the library's
	// arrivals folder as acquire and the catalog both see it, writable for
	// acquire: a finished download outside it is copied in before it is handed
	// to the catalog. "" hands every download over where it lies, as before.
	ArrivalsRoot string
	// DownloadsReserveBytes (ACQUIRE_DOWNLOADS_RESERVE, bytes) is kept free on
	// the volume the managed clients work on: no release is handed to one of
	// them that would leave less.
	DownloadsReserveBytes int64

	// Endpoint policy for URLs an admin enters (internal/endpoint).
	EndpointDeny          []string // ACQUIRE_ENDPOINT_DENY: comma list of hostnames or domain suffixes
	EndpointAllowInternal bool     // ACQUIRE_ENDPOINT_ALLOW_INTERNAL: allow services in other namespaces
	PodNamespace          string   // POD_NAMESPACE, else the service account's namespace file
	ClusterDomain         string   // the cluster DNS domain from /etc/resolv.conf; "" outside a cluster

	// The download clients the deployment runs itself (the addon chart's
	// clients.*), stored as managed clients at start: ACQUIRE_MANAGED_CLIENTS,
	// a JSON list of client writes, credentials included. ManagedClientsSet is
	// false when the variable is unset, and the stored clients are then the
	// console's alone; set but empty is a declaration of none.
	ManagedClients    string
	ManagedClientsSet bool

	// Subtitles in the instance's languages (internal/app/subtitles.go):
	// SubtitlesEnabled (ACQUIRE_SUBTITLES, default true) turns them on, which
	// does anything only with an OpenSubtitles account and API key
	// (OPENSUBTITLES_USERNAME, OPENSUBTITLES_PASSWORD, OPENSUBTITLES_API_KEY).
	// SubtitleLanguages (ACQUIRE_SUBTITLE_LANGUAGES, comma list) names the
	// languages; empty takes the platform's. SubtitleDailyBudget
	// (ACQUIRE_SUBTITLE_DAILY_BUDGET, default 18) is how many files the
	// automatic work downloads a day (UTC), under the account's quota so a
	// person's downloads in the console have some left.
	SubtitlesEnabled      bool
	SubtitleLanguages     []string
	SubtitleDailyBudget   int
	OpenSubtitlesUsername string
	OpenSubtitlesPassword string
	OpenSubtitlesAPIKey   string
	// Version (ACQUIRE_VERSION) is acquire's version, as the User-Agent of its
	// requests to a subtitle source names it.
	Version string
}

// DefaultSubtitleDailyBudget is the automatic downloads a day: a free
// OpenSubtitles account allows 20, and two are left for the console.
const DefaultSubtitleDailyBudget = 18

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
	managed, managedSet := os.LookupEnv("ACQUIRE_MANAGED_CLIENTS")
	return Config{
		Addr:         def(":8080", "ADDR"),
		OIDCIssuer:   env("OIDC_ISSUER"),
		OIDCAudience: def("chino", "OIDC_AUDIENCE"),
		AdminRole:    def("zaentrum-admin", "ACQUIRE_ADMIN_ROLE"),
		UserRole:     def("zaentrum-user", "ACQUIRE_USER_ROLE"),
		PlatformURL:  platformURL(env("ACQUIRE_PLATFORM_URL")),

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

		ArrivalsRoot:          strings.TrimRight(env("ACQUIRE_ARRIVALS_ROOT"), "/"),
		DownloadsReserveBytes: bytesOf(env("ACQUIRE_DOWNLOADS_RESERVE")),

		EndpointDeny:          splitList(env("ACQUIRE_ENDPOINT_DENY")),
		EndpointAllowInternal: env("ACQUIRE_ENDPOINT_ALLOW_INTERNAL") == "true",
		PodNamespace:          podNamespace(),
		ClusterDomain:         clusterDomain(),

		ManagedClients:    strings.TrimSpace(managed),
		ManagedClientsSet: managedSet,

		SubtitlesEnabled:      def("true", "ACQUIRE_SUBTITLES") != "false",
		SubtitleLanguages:     splitList(env("ACQUIRE_SUBTITLE_LANGUAGES")),
		SubtitleDailyBudget:   budgetOf(env("ACQUIRE_SUBTITLE_DAILY_BUDGET")),
		OpenSubtitlesUsername: env("OPENSUBTITLES_USERNAME"),
		OpenSubtitlesPassword: os.Getenv("OPENSUBTITLES_PASSWORD"),
		OpenSubtitlesAPIKey:   env("OPENSUBTITLES_API_KEY"),
		Version:               def("dev", "ACQUIRE_VERSION"),
	}
}

// budgetOf reads the daily download budget: a whole number from 0 up (0 lets
// the automatic work download nothing); anything else is the default.
func budgetOf(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return DefaultSubtitleDailyBudget
	}
	return n
}

// platformURL is the scheme and host of the platform's address, https when it
// names no scheme. Anything else of it (a path, a query) is dropped: acquire
// knows where the portal is on that host. "" when it names no host, or a scheme
// a browser cannot be sent to.
func platformURL(v string) string {
	if v == "" {
		return ""
	}
	if !strings.Contains(v, "://") {
		v = "https://" + v
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return u.Scheme + "://" + u.Host
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

// bytesOf reads a byte count; anything else is 0.
func bytesOf(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
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
